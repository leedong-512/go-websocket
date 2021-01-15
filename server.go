package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/gorilla/websocket"
	"html/template"
	"log"
	"net/http"
	"sync"
	"time"
)

// http升级websocket协议的配置
var wsUpgrader = websocket.Upgrader{
	// 支持跨域
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// 客户端读写消息
type wsMessage struct {
	messageType int
	data        []byte
}

type Data struct {
	Type int
	MsgData string
}

// 客户端连接
type wsConnection struct {
	wsSocket *websocket.Conn // 底层websocket
	inChan   chan *wsMessage // 读队列
	outChan  chan *wsMessage // 写队列

	mutex     sync.Mutex // 避免重复关闭管道
	isClosed  bool       // 管道是否已经关闭
	closeChan chan byte  // 关闭通知
}
var ch = make(chan *wsMessage, 1000)
// 写入消息
func (wsConn *wsConnection) wsWrite(messageType int, data []byte) error {
	select {
	case wsConn.outChan <- &wsMessage{messageType, data}:
	case <-wsConn.closeChan:
		return errors.New("websocket closed")
	}
	return nil
}

// 读取消息
func (wsConn *wsConnection) wsRead() (*wsMessage, error) {
	select {
	case msg := <-wsConn.inChan:
		return msg, nil
	case <-wsConn.closeChan:
		return nil, errors.New("websocket closed")
	}

}

// 关闭websocket连接
func (wsConn *wsConnection) wsClose() {
	wsConn.wsSocket.Close()

	wsConn.mutex.Lock()
	defer wsConn.mutex.Unlock()
	if !wsConn.isClosed {
		wsConn.isClosed = true
		close(wsConn.closeChan)
	}
}

// 循环读取
func (wsConn *wsConnection) wsReadLoop() {
	for {
		// 读一个message
		msgType, data, err := wsConn.wsSocket.ReadMessage()
		fmt.Println(msgType)
		fmt.Println(string(data))
		if err != nil {
			goto error
		}
		req := &wsMessage{
			messageType: msgType,
			data:        data,
		}

		// 请求放入队列
		select {
		case wsConn.inChan <- req:
		case <-wsConn.closeChan:
			goto closed
		}

	}
error:
	wsConn.wsClose()
closed:
}

// 循环写入
func (wsConn *wsConnection) wsWriteLoop() {
	for {
		select {
		// 取一个应答
		case msg := <-wsConn.outChan:
			// 写给websocket
			if err := wsConn.wsSocket.WriteMessage(msg.messageType, msg.data); err != nil {
				goto error
			}
		case <-wsConn.closeChan:
			goto closed
		}
	}
error:
	wsConn.wsClose()
closed:
}

// 发送存活心跳
func (wsConn *wsConnection) procLoop() {
	// 启动一个gouroutine发送心跳
	go func() {
		data := &Data{}
		data.MsgData = "hello"
		data.Type = 2
		json_data,_ := json.Marshal(data)
		for {
			time.Sleep(2 * time.Second)
			if err := wsConn.wsWrite(websocket.TextMessage, json_data); err != nil {
				fmt.Println("heartbeat fail")
				wsConn.wsClose()
				break
			}
		}
	}()

	// 这是一个同步处理模型（只是一个例子），如果希望并行处理可以每个请求一个gorutine，注意控制并发goroutine的数量!!!
	for {
		msg, err := wsConn.wsRead()
		if err != nil {
			fmt.Println("read fail")
			break
		}
		data := Data{}
		data.MsgData = string(msg.data)
		data.Type = msg.messageType
		jsonData, _ := json.Marshal(data)
		fmt.Println("----111----")
		fmt.Println(string(jsonData))
		err = wsConn.wsWrite(websocket.TextMessage, jsonData)
		if err != nil {
			fmt.Println("write fail")
			break
		}
	}
}

//php 推送数据
func (wsConn *wsConnection) push(ch chan *wsMessage) {
	for {
		select {
		// 取一个应答
		case msg := <- ch:
			// 写给websocket
			if err := wsConn.wsSocket.WriteMessage(msg.messageType, msg.data); err != nil {
				goto error
			}
		case <-wsConn.closeChan:
			goto closed
		}
	}
error:
	wsConn.wsClose()
closed:
}

func wsHandler(resp http.ResponseWriter, req *http.Request) {
	// 应答客户端告知升级连接为websocket
	wsSocket, err := wsUpgrader.Upgrade(resp, req, nil)
	if err != nil {
		return
	}
	// 初始化wsConn连接
	wsConn := &wsConnection{
		wsSocket:  wsSocket,
		inChan:    make(chan *wsMessage, 1000),
		outChan:   make(chan *wsMessage, 1000),
		closeChan: make(chan byte),
		isClosed:  false,
	}

	//wsConn.wsWrite()
	// 处理器
	go wsConn.procLoop()
	// 读协程
	go wsConn.wsReadLoop()
	// 写协程
	go wsConn.wsWriteLoop()
	//获取PHP推送数据
	go wsConn.push(ch)
}


func phpClient(resp http.ResponseWriter, req *http.Request)  {
	req.ParseForm()
	fmt.Fprintln(resp,req.Form)
	/*
		按照请求参数名获取参数值
		根据源码,FormValue(key)=req.Form[key]
	*/
	name:=req.FormValue("name")
	age:=req.FormValue("age")
	fmt.Fprintln(resp,name,age)
	data := Data{}
	data.MsgData = "test"
	data.Type = 1
	jsonData, _ := json.Marshal(data)
	ch <- &wsMessage{messageType: websocket.TextMessage, data:jsonData}
	fmt.Println(string(jsonData))
}

func main() {
	var port string
	flag.StringVar(&port, "p", "2021", "server port")
	flag.Parse()
	http.HandleFunc("/ws", wsHandler)
	//test client
	http.HandleFunc("/index", func(w http.ResponseWriter, r *http.Request) {
		t, err := template.ParseFiles("./html/index.html")
		if err != nil {
			log.Println("err:",err)
			return
		}
		_ = t.Execute(w, nil)
	})
	//php client
	http.HandleFunc("/php", phpClient)
	fmt.Println(":" + port)
	_ = http.ListenAndServe(":"+port, nil)
}