package transport

import (
	"stash.cloudflare.com/go-stream/stream"
	"stash.cloudflare.com/go-stream/stream/sink"
	"stash.cloudflare.com/go-stream/stream/source"
	"log"
	"net"
	"sync"
	"time"
)

type Server struct {
	outch             chan []byte
	addr              string
	hwm               int
	hardCloseListener chan bool
}

func DefaultServer() (Server, chan []byte) {
	return NewServer(":4558", DEFAULT_HWM)
}

func NewServer(addr string, highWaterMark int) (Server, chan []byte) {
	outputch := make(chan []byte, stream.CHAN_SLACK)
	hcl := make(chan bool)
	zmqsrc := Server{outputch, addr, highWaterMark, hcl}

	return zmqsrc, outputch
}

func (src Server) Stop() error {
	close(src.hardCloseListener)
	return nil
}

func hardCloseListener(hcn chan bool, sfc chan bool, listener net.Listener) {
	select {
	case <-hcn:
		//log.Println("HC Closing server listener")
		listener.Close()
	case <-sfc:
		//log.Println("SC Closing server listener")
		listener.Close()
	}
}

func (src Server) Run() error {
	defer close(src.outch)

	ln, err := net.Listen("tcp", src.addr)
	if err != nil {
		log.Println("Error listening", err)
		return err
	}

	wg_sub := &sync.WaitGroup{}
	defer wg_sub.Wait()

	scl := make(chan bool)
	defer close(scl)

	wg_sub.Add(1)
	go func() {
		defer wg_sub.Done()
		hardCloseListener(src.hardCloseListener, scl, ln)
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			hardClose := false
			select {
			case _, ok := <-src.hardCloseListener:
				if !ok {
					hardClose = true
				}
			default:
			}
			if !hardClose {
				log.Println("Accept Error", err)
			}
			return nil
		}
		wg_sub.Add(1)
		go func() {
			defer wg_sub.Done()
			defer conn.Close() //handle connection will close conn because of reader and writer. But just as good coding practice
			src.handleConnection(conn)
		}()
	}

}

func (src Server) handleConnection(conn net.Conn) {
	wg_sub := &sync.WaitGroup{}
	defer wg_sub.Wait()

	sndChData := make(chan stream.Object, 100)
	sndChCloseNotifier := make(chan bool, 1)
	defer close(sndChData)
	//side effect: this will close conn on exit
	sender := sink.NewMultiPartWriterSink(conn)
	sender.SetIn(sndChData)
	wg_sub.Add(1)
	go func() {
		defer wg_sub.Done()
		defer close(sndChCloseNotifier)
		err := sender.Run()
		if err != nil {
			log.Println("Error in server sender", err)
		}
	}()
	defer sender.Stop()

	//this will actually close conn too
	rcvChData := make(chan stream.Object, 100)
	receiver := source.NewIOReaderSourceLengthDelim(conn)
	receiver.SetOut(rcvChData)
	rcvChCloseNotifier := make(chan bool, 1)
	wg_sub.Add(1)
	go func() {
		defer wg_sub.Done()
		defer close(rcvChCloseNotifier)
		err := receiver.Run()
		if err != nil {
			log.Println("Error in server reciever", err)
		}
	}()
	defer receiver.Stop()

	lastGotAck := 0
	lastSentAck := 0
	var timer <-chan time.Time
	timer = nil
	for {
		select {
		case obj, ok := <-rcvChData:
			command, seq, payload, err := parseMsg(obj.([]byte))

			if !ok {
				//send last ack back??
			}

			if err == nil {
				if command == DATA {
					lastGotAck = seq
					if (lastGotAck - lastSentAck) > src.hwm/2 {
						sendAck(sndChData, lastGotAck)
						lastSentAck = lastGotAck
						timer = nil
					} else {
						timer = time.After(100 * time.Millisecond)
					}
					src.outch <- payload
				} else if command == CLOSE {
					if lastGotAck > lastSentAck {
						sendAck(sndChData, lastGotAck)
					}
					return
				} else {
					log.Fatal("Server Got Unknown Command")
				}
			} else {
				log.Fatal("Server could not parse packet", err)
			}
		case <-rcvChCloseNotifier:
			log.Println("Client asked for a close on recieve- should not happen")
			return
		case <-sndChCloseNotifier:
			log.Println("Server asked for a close on send - should not happen")
			return
		case <-timer:
			sendAck(sndChData, lastGotAck)
			lastSentAck = lastGotAck
			timer = nil
		case <-src.hardCloseListener:
			return
		}

	}
}
