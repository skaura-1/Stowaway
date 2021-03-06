package admin

import (
	"Stowaway/common"
	"bufio"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

var (
	AdminStuff     *common.AdminStuff
	NodeStatus     *common.NodeStatus
	ForwardStatus  *common.ForwardStatus
	ReflectConnMap *common.Uint32ConnMap
	PortReflectMap *common.Uint32ChanStrMap
)

func init() {
	ReflectConnMap = common.NewUint32ConnMap()
	PortReflectMap = common.NewUint32ChanStrMap()
	NodeStatus = common.NewNodeStatus()
	ForwardStatus = common.NewForwardStatus()
	AdminStuff = common.NewAdminStuff()
}

/*-------------------------控制台相关代码--------------------------*/
// 启动控制台
func Controlpanel() {
	inputReader := bufio.NewReader(os.Stdin)
	var command string
	for {
		fmt.Printf("(%s) >> ", *CliStatus)
		input, err := inputReader.ReadString('\n')
		if err != nil {
			fmt.Println(err)
			continue
		}
		if runtime.GOOS == "windows" {
			command = strings.Replace(input, "\r\n", "", -1)
		} else {
			command = strings.Replace(input, "\n", "", -1)
		}
		execCommand := strings.Split(command, " ")
		AdminStuff.AdminCommandChan <- execCommand

		<-AdminStatus.ReadyChange
		<-AdminStatus.IsShellMode
	}
}

/*-------------------------Socks5功能相关代码--------------------------*/
// 启动socks5 for client
func StartSocksServiceForClient(command []string, startNodeConn net.Conn, nodeID uint32) {
	var err error

	Route.Lock()
	route := Route.Route[nodeID]
	Route.Unlock()

	socksPort := command[1]
	checkPort, _ := strconv.Atoi(socksPort)
	if checkPort <= 0 || checkPort > 65535 {
		log.Println("[*]Port Illegal!")
		return
	}

	socks5Addr := fmt.Sprintf("0.0.0.0:%s", socksPort)
	socksListenerForClient, err := net.Listen("tcp", socks5Addr)
	if err != nil {
		respCommand, _ := common.ConstructPayload(nodeID, route, "COMMAND", "SOCKSOFF", " ", " ", 0, 0, AdminStatus.AESKey, false)
		_, err = startNodeConn.Write(respCommand)
		if err != nil {
			log.Println("[*]Cannot stop agent's socks service,check the connection!")
		}
		log.Println("[*]Cannot listen this port!")
		return
	}
	AdminStuff.SocksListenerForClient = append(AdminStuff.SocksListenerForClient, socksListenerForClient)
	for {
		conn, err := socksListenerForClient.Accept()
		if err != nil {
			log.Println("[*]Socks service stopped")
			return
		}
		ClientSockets.Lock()
		ClientSockets.Payload[AdminStuff.SocksNum] = conn
		go HandleNewSocksConn(startNodeConn, ClientSockets.Payload[AdminStuff.SocksNum], AdminStuff.SocksNum, nodeID)
		ClientSockets.Unlock()
		AdminStuff.Lock()
		AdminStuff.SocksNum++
		AdminStuff.Unlock()
	}
}

//处理每一个单个的socks socket
func HandleNewSocksConn(startNodeConn net.Conn, clientsocks net.Conn, num uint32, nodeID uint32) {
	Route.Lock()
	route := Route.Route[nodeID]
	Route.Unlock()

	buffer := make([]byte, 10240)
	for {
		len, err := clientsocks.Read(buffer)
		if err != nil {
			clientsocks.Close()
			finMessage, _ := common.ConstructPayload(nodeID, route, "DATA", "FIN", " ", " ", num, 0, AdminStatus.AESKey, false)
			startNodeConn.Write(finMessage)
			return
		} else {
			respData, _ := common.ConstructPayload(nodeID, route, "DATA", "SOCKSDATA", " ", string(buffer[:len]), num, 0, AdminStatus.AESKey, false)
			startNodeConn.Write(respData)
		}
	}
}

/*-------------------------Ssh功能启动相关代码--------------------------*/
// 发送ssh开启命令
func StartSSHService(startNodeConn net.Conn, info []string, nodeid uint32) {
	information := fmt.Sprintf("%s::%s::%s", info[1], info[2], info[3])
	Route.Lock()
	sshCommand, _ := common.ConstructPayload(nodeid, Route.Route[nodeid], "COMMAND", "SSH", " ", information, 0, 0, AdminStatus.AESKey, false)
	Route.Unlock()
	startNodeConn.Write(sshCommand)
}

/*-------------------------Port Forward功能启动相关代码--------------------------*/
// 发送forward开启命令
func HandleForwardPort(forwardconn net.Conn, target string, startNodeConn net.Conn, num uint32, nodeid uint32) {
	Route.Lock()
	route := Route.Route[nodeid]
	Route.Unlock()

	forwardCommand, _ := common.ConstructPayload(nodeid, route, "DATA", "FORWARD", " ", target, num, 0, AdminStatus.AESKey, false)
	startNodeConn.Write(forwardCommand)

	buffer := make([]byte, 10240)
	for {
		len, err := forwardconn.Read(buffer)
		if err != nil {
			finMessage, _ := common.ConstructPayload(nodeid, route, "DATA", "FORWARDFIN", " ", " ", num, 0, AdminStatus.AESKey, false)
			startNodeConn.Write(finMessage)
			return
		} else {
			respData, _ := common.ConstructPayload(nodeid, route, "DATA", "FORWARDDATA", " ", string(buffer[:len]), num, 0, AdminStatus.AESKey, false)
			startNodeConn.Write(respData)
		}
	}
}

func StartPortForwardForClient(info []string, startNodeConn net.Conn, nodeid uint32, AESKey []byte) {
	TestIfValid("FORWARDTEST", startNodeConn, info[2], nodeid)
	if <-ForwardStatus.ForwardIsValid {
	} else {
		return
	}

	localPort := info[1]
	forwardAddr := fmt.Sprintf("0.0.0.0:%s", localPort)
	forwardListenerForClient, err := net.Listen("tcp", forwardAddr)
	if err != nil {
		log.Println("[*]Cannot forward this local port!")
		return
	}

	ForwardStatus.CurrentPortForwardListener = append(ForwardStatus.CurrentPortForwardListener, forwardListenerForClient)

	for {
		conn, err := forwardListenerForClient.Accept()
		if err != nil {
			log.Println("[*]PortForward service stopped")
			return
		}
		PortForWardMap.Lock()
		PortForWardMap.Payload[ForwardStatus.ForwardNum] = conn
		go HandleForwardPort(PortForWardMap.Payload[ForwardStatus.ForwardNum], info[2], startNodeConn, ForwardStatus.ForwardNum, nodeid)
		PortForWardMap.Unlock()
		ForwardStatus.Lock()
		ForwardStatus.ForwardNum++
		ForwardStatus.Unlock()
	}
}

func StopForward() {
	for _, listener := range ForwardStatus.CurrentPortForwardListener {
		listener.Close()
	}
}

/*-------------------------Reflect Port相关代码--------------------------*/
//测试agent是否能够监听
func StartReflectForClient(info []string, startNodeConn net.Conn, nodeid uint32, AESKey []byte) {
	tempInfo := fmt.Sprintf("%s:%s", info[1], info[2])
	TestIfValid("REFLECTTEST", startNodeConn, tempInfo, nodeid)
}

func TryReflect(startNodeConn net.Conn, nodeid uint32, id uint32, port string) {
	target := fmt.Sprintf("127.0.0.1:%s", port)
	reflectConn, err := net.Dial("tcp", target)
	if err == nil {
		ReflectConnMap.Lock()
		ReflectConnMap.Payload[id] = reflectConn
		ReflectConnMap.Unlock()
	} else {
		Route.Lock()
		respdata, _ := common.ConstructPayload(nodeid, Route.Route[nodeid], "DATA", "REFLECTTIMEOUT", " ", " ", id, 0, AdminStatus.AESKey, false)
		Route.Unlock()
		startNodeConn.Write(respdata)
		return
	}
}

func HandleReflect(startNodeConn net.Conn, reflectDataChan chan string, num uint32, nodeid uint32) {
	ReflectConnMap.RLock()
	reflectConn := ReflectConnMap.Payload[num]
	ReflectConnMap.RUnlock()

	Route.Lock()
	route := Route.Route[nodeid]
	Route.Unlock()

	go func() {
		for {
			reflectData, ok := <-reflectDataChan
			if ok {
				reflectConn.Write([]byte(reflectData))
			} else {
				return
			}
		}
	}()

	go func() {
		serverbuffer := make([]byte, 10240)
		for {
			len, err := reflectConn.Read(serverbuffer)
			if err != nil {
				respdata, _ := common.ConstructPayload(nodeid, route, "DATA", "REFLECTOFFLINE", " ", " ", num, 0, AdminStatus.AESKey, false)
				startNodeConn.Write(respdata)
				return
			}
			respdata, _ := common.ConstructPayload(nodeid, route, "DATA", "REFLECTDATARESP", " ", string(serverbuffer[:len]), num, 0, AdminStatus.AESKey, false)
			startNodeConn.Write(respdata)
		}
	}()
}

func StopReflect(startNodeConn net.Conn, nodeid uint32) {
	fmt.Println("[*]Stop command has been sent")
	Route.Lock()
	command, _ := common.ConstructPayload(nodeid, Route.Route[nodeid], "COMMAND", "STOPREFLECT", " ", " ", 0, 0, AdminStatus.AESKey, false)
	Route.Unlock()
	startNodeConn.Write(command)
}

/*-------------------------一些功能相关代码--------------------------*/
//测试是否端口可用
func TestIfValid(commandtype string, startNodeConn net.Conn, target string, nodeid uint32) {
	Route.Lock()
	command, _ := common.ConstructPayload(nodeid, Route.Route[nodeid], "COMMAND", commandtype, " ", target, 0, 0, AdminStatus.AESKey, false)
	Route.Unlock()
	startNodeConn.Write(command)
}

//拆分Info
func AnalysisInfo(info string) (string, uint32) {
	spiltInfo := strings.Split(info, ":::")
	upperNode := common.StrUint32(spiltInfo[0])
	ip := spiltInfo[1]
	return ip, upperNode
}

/*-------------------------功能控制相关代码--------------------------*/
//捕捉退出信号
func MonitorCtrlC(startNodeConn net.Conn) {
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	<-signalChan
	os.Exit(1)
}

//当有一个节点下线，强制关闭所有的服务
func CloseAll() {
	ClientSockets.Lock()
	for key, conn := range ClientSockets.Payload {
		conn.Close()
		delete(ClientSockets.Payload, key)
	}
	ClientSockets.Unlock()
	for _, listener := range AdminStuff.SocksListenerForClient {
		err := listener.Close()
		if err != nil {
		}
	}

	StopForward()
	PortForWardMap.Lock()
	for key, conn := range PortForWardMap.Payload {
		conn.Close()
		delete(PortForWardMap.Payload, key)
	}
	PortForWardMap.Unlock()

	ReflectConnMap.Lock()
	for key, conn := range ReflectConnMap.Payload {
		conn.Close()
		delete(ReflectConnMap.Payload, key)
	}
	ReflectConnMap.Unlock()

	PortReflectMap.Lock()
	for key, channel := range PortReflectMap.Payload {
		close(channel)
		delete(PortReflectMap.Payload, key)
	}
	PortReflectMap.Unlock()
}
