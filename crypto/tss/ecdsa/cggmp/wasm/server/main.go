//go:build js && wasm
// +build js,wasm

package main

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/getamis/alice/crypto/ecpointgrouplaw"
	"github.com/getamis/alice/crypto/elliptic"
	"github.com/getamis/alice/crypto/tss/ecdsa/cggmp/dkg"
	"github.com/getamis/alice/crypto/tss/ecdsa/cggmp/refresh"
	"github.com/getamis/alice/crypto/tss/ecdsa/cggmp/sign"
	"github.com/getamis/alice/types"
	"github.com/gorilla/websocket"

	// host "github.com/libp2p/go-libp2p-host"

	"github.com/pkg/errors"
	"google.golang.org/protobuf/proto"
)

// var dkgP = &dkg.DKG{}

// var wsconn *websocket.Conn
var gLocker = &sync.RWMutex{}
var allGroup = map[string]*groupData{}

func loginfo(format string, args ...any) {
	s := fmt.Sprintf(format, args...)
	fmt.Println(s)
}

type groupData struct {
	dkg         *dkg.DKG
	dkgResult   *dkg.Result
	refre       *refresh.Refresh
	refreResult *refresh.Result
	signer      *sign.Sign
	wsconn      *websocket.Conn
	locker      *sync.RWMutex
	l           *listener
	pPKs        map[string]ecpointgrouplaw.ECPoint
}

type peerManager struct {
	group *groupData
	id    string
	ids   []string
}

func NewPeerManager(selfID string, ids []string, g *groupData) *peerManager {
	return &peerManager{
		group: g,
		id:    selfID,
		ids:   ids,
	}
}

func (p *peerManager) NumPeers() uint32 {
	return uint32(len(p.ids))
}

func (p *peerManager) SelfID() string {
	return p.id
}

func (p *peerManager) PeerIDs() []string {
	return p.ids
}

func (p *peerManager) MustSend(peerId string, message interface{}) {
	msg, ok := message.(proto.Message)
	if !ok {
		loginfo("invalid proto message")
		return
	}

	bs, err := proto.Marshal(msg)
	if err != nil {
		loginfo("Cannot marshal message, err %v", err)
		return
	}
	bs = append([]byte{1}, bs...)
	p.group.locker.Lock()
	if p.group.wsconn != nil {
		wMsg := map[string]string{}
		wMsg["data"] = base64.StdEncoding.EncodeToString(bs)
		wMsg["receiverid"] = peerId
		err := p.group.wsconn.WriteJSON(wMsg)
		loginfo("Error send: %v", err)
		fmt.Printf("Trying to send %v %v\n", wMsg, err)
	}
	p.group.locker.Unlock()
}

// EnsureAllConnected connects the host to specified peer and sends the message to it.
func (p *peerManager) EnsureAllConnected() {
}

// AddPeer adds a peer to the peer list.
func (p *peerManager) AddPeer(peerId string, peerAddr string) {
	// p.peers[peerId] = peerAddr
}

type listener struct {
	errCh chan error
}

func (l *listener) OnStateChanged(oldState types.MainState, newState types.MainState) {
	if newState == types.StateFailed {
		l.errCh <- fmt.Errorf("State %s -> %s", oldState.String(), newState.String())
		return
	} else if newState == types.StateDone {
		l.errCh <- nil
		return
	}

	// log.Debug("State changed", "old", oldState.String(), "new", newState.String())
}

func (l *listener) Done() <-chan error {
	return l.errCh
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Accepting all requests
	},
}

type Message interface {
	types.Message
	proto.Message
}

// Start the WebSocket server
func startServer() {

	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		// var data Message
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			loginfo("Error connecting to WebSocket server:%v", err)
			// log.Fatal("Error upgrading to WebSocket:", err)
		}
		defer conn.Close()

		websocketID := conn.RemoteAddr().String()
		teleID := ""
		gData := &groupData{}
		loginfo("Start listening client %v\n", websocketID)
		for {
			msgTmp := []byte{}
			_, msgTmp, err := conn.ReadMessage()
			if err != nil {
				fmt.Printf("Websocket ID %v return err %v", websocketID, err)
				return
			}
			loginfo("Got msg %v %v from %v", msgTmp, len(msgTmp), websocketID)
			senderID := msgTmp[0]
			msgTmp = msgTmp[1:]
			msgCmd := msgTmp[0]
			msgTmp = msgTmp[1:]
			switch msgCmd {
			case 0:
				fmt.Printf("Receive msg start, %v %v %v\n", senderID, msgCmd, string(msgTmp))
				err := NewDKGPerTelegramID(string(msgTmp), "", conn)
				if err != nil {
					fmt.Printf("Cannot create new dkg err %v\n", err)
					return
				}
				teleID = string(msgTmp)
				gLocker.RLock()
				gData = allGroup[teleID]
				gLocker.RUnlock()
				wMsg := map[string]string{}
				wMsg["data"] = base64.StdEncoding.EncodeToString([]byte{0, 0, 0})
				wMsg["receiverid"] = "client1"
				go StartDKGPerTelegramID(teleID)
				err = conn.WriteJSON(wMsg)
				if err != nil {
					fmt.Printf("Cannot write json %v\n", err)
					// wsconn = nil
					return
				}

			case 1:
				fmt.Printf("data get from client %v %v \n", senderID, msgTmp)
				data := &dkg.Message{}
				err = proto.Unmarshal(msgTmp, data)
				if err != nil {
					fmt.Printf("Cannot proto unmarshal data err %v\n", err)
					// wsconn = nil
					return
				}
				loginfo("sender id %v msgtype %v %v %v", err, senderID, msgCmd, msgTmp)
				// if err != nil {
				// 	err := errors.Errorf("Error reading message:%v\n", err)
				// 	loginfo("Error reading message:%v\n", err)
				// 	continue
				// }

				loginfo("----------- sender %v %v -----------", fmt.Sprintf("client%v", senderID), gData.dkg.AddMessage(fmt.Sprintf("client%v", senderID), data))
			case 2:
				fmt.Printf("data get from client %v %v \n", senderID, msgTmp)
				var msg ecpointgrouplaw.EcPointMessage
				err = proto.Unmarshal(msgTmp, &msg)
				if err != nil {
					loginfo("Cannot unmarshal proto message", "err", err)
					return
				}

				p, err := msg.ToPoint()
				if err != nil {
					loginfo("Cannot convert to EcPoint", "err", err)
					return
				}
				gData.pPKs[fmt.Sprintf("client%v", senderID)] = *p
				loginfo("----------- DONE -----------")
			}
			// fmt.Printf("msgtype %v \n", dkgP.GetHandler().MessageType())

		}
		// wsconn = nil
	})

	loginfo("Start Server %v", http.ListenAndServe(":8080", nil))
}

func NewDKGPerTelegramID(teleID string, sID string, wsconn *websocket.Conn) error {
	gLocker.RLock()
	gData, ok := allGroup[teleID]
	if !ok {
		fmt.Println("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		gData = &groupData{
			locker: &sync.RWMutex{},
			l: &listener{
				errCh: make(chan error, 10),
			},
			pPKs: make(map[string]ecpointgrouplaw.ECPoint),
		}
	}
	gLocker.RUnlock()

	sID = "aaaaaaaaaaaaa"
	dkgP, err := dkg.NewDKG(elliptic.Secp256k1(), NewPeerManager("client2", []string{"client1", "client3"}, gData), []byte(sID), 2, 0, gData.l)
	if err != nil {
		return err
	}
	gData.dkg = dkgP
	gData.wsconn = wsconn
	gLocker.Lock()
	allGroup[teleID] = gData
	gLocker.Unlock()

	return nil
}

func StartDKGPerTelegramID(teleID string) error {
	gLocker.RLock()
	gData, ok := allGroup[teleID]
	gLocker.RUnlock()
	if !ok {
		return errors.Errorf("DKG for tele %v is not init yet", teleID)
	}
	gData.dkg.Start()
	defer gData.dkg.Stop()
	time.Sleep(500 * time.Millisecond)
	gData.dkg.Start2()
	gData.dkg.Start2()
	if err := <-gData.l.Done(); err != nil {
		panic(err)
	} else {
		loginfo("DKG done!\n")
		gData.dkgResult, err = gData.dkg.GetResult()
		if err != nil {
			return err
		}
		p := *ecpointgrouplaw.ScalarBaseMult(elliptic.Secp256k1(), gData.dkgResult.Share)
		gData.pPKs["client2"] = p

		msgPPK := []byte{}
		msgPPK = append(msgPPK, 2)
		pkPointMsg, err := p.ToEcPointMessage()
		pkBytes, err := proto.Marshal(pkPointMsg)
		if err != nil {
			return err
		}
		msgPPK = append(msgPPK, pkBytes...)
		if gData.wsconn != nil {
			wMsg := map[string]string{}
			wMsg["data"] = base64.StdEncoding.EncodeToString(msgPPK)
			wMsg["receiverid"] = "client1"
			err := gData.wsconn.WriteJSON(wMsg)
			loginfo("Error send: %v", err)
			fmt.Printf("Trying to send %v %v\n", wMsg, err)
		}
	}
	gLocker.Lock()
	allGroup[teleID] = gData
	gLocker.Unlock()
	return nil
}

func main() {
	// var err error
	go startServer()

	// dkgP, err = dkg.NewDKG(elliptic.Secp256k1(), NewPeerManager("client2", []string{"client1", "client3"}), []byte("aaaaaaaaaaaaa"), 2, 0, l1)
	// loginfo("DKG return err %v", err)
	// dkgP.Start()
	// defer dkgP.Stop()

	// // select {}
	// fmt.Println("Start")
	// if err := <-l1.Done(); err != nil {
	// 	panic(err)
	// } else {
	// 	fmt.Printf("na ni\n")
	// }
	// // if err := <-l2.Done(); err != nil {
	// // 	panic(err)
	// // } else {
	// // 	fmt.Printf("ffffffffffffffffffff ni\n")
	// // }
	// result1, _ := dkgP.GetResult()
	// // result2, _ := dkg2.GetResult()
	// myPartialPublicKey1 := ecpointgrouplaw.ScalarBaseMult(elliptic.Secp256k1(), result1.Share)
	// // myPartialPublicKey2 := ecpointgrouplaw.ScalarBaseMult(elliptic.Secp256k1(), result2.Share)
	// fmt.Printf("vkvmdfo;jnsdgoibn  %v \n", myPartialPublicKey1.ToPubKey())
	select {}
}
