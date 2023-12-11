//go:build js && wasm
// +build js,wasm

package main

import (
	"crypto/ecdsa"
	"fmt"
	"syscall/js"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/getamis/alice/crypto/ecpointgrouplaw"
	"github.com/getamis/alice/crypto/elliptic"
	"github.com/getamis/alice/crypto/tss/ecdsa/cggmp/dkg"
	"github.com/getamis/alice/types"
	"github.com/pkg/errors"
	"google.golang.org/protobuf/proto"
)

var (
	GlobalMsg = make(chan []byte, 10)
	PKs       = map[string]ecpointgrouplaw.ECPoint{}
	ready     = make(chan bool, 2)
	pPK       = make(chan []byte, 2)
	dkg1      *dkg.DKG
	l1        *listener
	dkg3      *dkg.DKG
	l3        *listener
)

func JSSend(msg []byte, senderID string) error {
	wMsg := []byte{}
	switch senderID {
	case "client1":
		wMsg = append(wMsg, 1)
	case "client3":
		wMsg = append(wMsg, 3)
	default:
		wMsg = append(wMsg, 0)
	}

	wMsg = append(wMsg, msg...)
	uint8Array := js.Global().Get("Uint8Array").New(len(wMsg))
	js.CopyBytesToJS(uint8Array, wMsg)
	js.Global().Call("onWasmEvent", uint8Array)
	return nil
}

func JSCheckConnect(serverID string) bool {
	connected := js.Global().Get(serverID + "connected")
	if connected.IsNull() {
		return false
	}
	return true
}

//export JSReceive
func JSReceive(receiverID string, data []byte) error {
	loginfo("%v got data %v", receiverID, []byte(data))
	msg := &dkg.Message{}
	if data[0] == 0 {
		loginfo("kkkkkkkk Received msg start, data %v", data)
		ready <- true
		return nil
	}
	if data[0] == 1 {
		loginfo("kkkkkkkk Received msg tss, data %v", data)
		data = data[1:]
		err := proto.Unmarshal([]byte(data), msg)
		if err != nil {
			loginfo("Error proto.Unmarshal: %v", err)
			return err
		}
		if receiverID == "client1" {
			return dkg1.AddMessage("client2", msg)
		}
		if receiverID == "client3" {
			return dkg3.AddMessage("client2", msg)
		}
		return nil
	}
	if data[0] == 2 {
		loginfo("kkkkkkkk Received msg pk, data %v", data)
		data = data[1:]
		GlobalMsg <- data
		return nil
	}
	loginfo("kkkkkkkk WTF %v", data)
	return nil
}

func loginfo(format string, args ...any) {
	s := fmt.Sprintf(format, args...)
	js.Global().Get("console").Call("log", s)
}

func main() {
	js.Global().Set("JSReceive", js.FuncOf(JSReceiveWrapper))
	InitKeyGen("aaaaaaaaaaaaa")
	loginfo("DKG start")
	// defer dkg1.Stop()
	// defer dkg2.Stop()
	KeyGen("okokokokook")
	select {}
}

func InitKeyGen(sid string) {
	var err1 error
	var err3 error
	l1 = &listener{
		errCh: make(chan error, 10),
	}
	l3 = &listener{
		errCh: make(chan error, 10),
	}
	dkg1, err1 = dkg.NewDKG(elliptic.Secp256k1(), NewPeerManager("client1", []string{"client2", "client3"}), []byte(sid), 2, 0, l1)
	loginfo("DKG return err %v", err1)
	dkg3, err3 = dkg.NewDKG(elliptic.Secp256k1(), NewPeerManager("client3", []string{"client1", "client2"}), []byte(sid), 2, 0, l3)
	loginfo("DKG return err %v", err3)
}
func KeyGen(telegramID string) (*ecdsa.PublicKey, error) {
	if dkg1 == nil || dkg3 == nil {
		loginfo("DKG is not init yet")
		return nil, errors.Errorf("DKG is not init yet")
	}
	go func() {
		dkg1.Start()
	}()
	go func() {
		dkg3.Start()
	}()
	defer dkg1.Stop()
	defer dkg3.Stop()
	msgStart := []byte{}
	msgStart = append(msgStart, 0)
	msgStart = append(msgStart, []byte(telegramID)...)
	JSSend(msgStart, telegramID)
	loginfo("Waiting for result %v 1", time.Now())
	<-ready
	loginfo("Waiting for result %v 2", time.Now())
	st := time.Now()
	dkg1.Start2()
	dkg3.Start2()
	dkg1.Start2()
	dkg3.Start2()
	loginfo("Waiting for result")
	if err := <-l1.Done(); err != nil {
		return nil, err
	} else {
		loginfo("DKG 1 done!\n")
	}
	if err := <-l3.Done(); err != nil {
		return nil, err
	} else {
		fmt.Printf("DKG 3 done\n")
	}
	result1, _ := dkg1.GetResult()
	result3, _ := dkg3.GetResult()
	myPartialPublicKey1 := ecpointgrouplaw.ScalarBaseMult(elliptic.Secp256k1(), result1.Share)
	msgPPK := []byte{}
	msgPPK = append(msgPPK, 2)
	pkPointMsg, err := myPartialPublicKey1.ToEcPointMessage()
	pkBytes, err := proto.Marshal(pkPointMsg)
	if err != nil {
		return nil, err
	}
	msgPPK = append(msgPPK, pkBytes...)
	JSSend(msgPPK, "client1")
	myPartialPublicKey3 := ecpointgrouplaw.ScalarBaseMult(elliptic.Secp256k1(), result3.Share)

	msgPPK = []byte{}
	msgPPK = append(msgPPK, 2)
	pkPointMsg, err = myPartialPublicKey3.ToEcPointMessage()
	pkBytes, err = proto.Marshal(pkPointMsg)
	if err != nil {
		return nil, err
	}
	msgPPK = append(msgPPK, pkBytes...)
	JSSend(msgPPK, "client3")
	serverPK := <-GlobalMsg
	var msg ecpointgrouplaw.EcPointMessage
	err = proto.Unmarshal(serverPK, &msg)
	if err != nil {
		loginfo("Cannot unmarshal proto message", "err", err)
		return nil, err
	}

	p, err := msg.ToPoint()
	if err != nil {
		loginfo("Cannot convert to EcPoint", "err", err)
		return nil, err
	}
	PKs["client2"] = *p
	PKs["client1"] = *myPartialPublicKey1
	PKs["client3"] = *myPartialPublicKey3

	loginfo("Keygen done, server pk %v, got %v cost %v", p.String(), crypto.PubkeyToAddress(*result1.PublicKey.ToPubKey()), time.Since(st))
	return result1.PublicKey.ToPubKey(), nil
}

type peerManager struct {
	id  string
	ids []string
}

func NewPeerManager(selfID string, ids []string) *peerManager {
	return &peerManager{
		id:  selfID,
		ids: ids,
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
	loginfo("Trying to send %v %v %v %v", peerId, message.(types.Message).GetMessageType(), bs, len(bs))
	if peerId == "client1" {
		err = dkg1.AddMessage(p.SelfID(), message.(types.Message))
		loginfo("Trying to send %v %v to dkg1", p.SelfID(), peerId)
		return
	}
	if peerId == "client2" {
		bs = append([]byte{1}, bs...)
		err := JSSend(bs, p.SelfID())
		loginfo("JSSend err %v", err)
		return
	}
	if peerId == "client3" {
		err = dkg3.AddMessage(p.SelfID(), message.(types.Message))
		fmt.Printf("Trying to send %v %v to dkg3", p.SelfID(), peerId)
		return
	}
}

// EnsureAllConnected connects the host to specified peer and sends the message to it.
func (p *peerManager) EnsureAllConnected() {
	// JSCheckConnect(p.serverID)
}

// AddPeer adds a peer to the peer list.
func (p *peerManager) AddPeer(peerId string, peerAddr string) {
}

type listener struct {
	errCh chan error
}

func (l *listener) OnStateChanged(oldState types.MainState, newState types.MainState) {
	loginfo("State changed; old", oldState.String(), "new", newState.String())
	if newState == types.StateFailed {
		l.errCh <- fmt.Errorf("State %s -> %s", oldState.String(), newState.String())
		return
	} else if newState == types.StateDone {
		l.errCh <- nil
		return
	}
}

func (l *listener) Done() <-chan error {
	return l.errCh
}

// func KeyGen() interface{} {
// 	receiverID := p[0].String()
// 	data := p[1]
// 	dataBytes := jsValueToBytes(data)

// 	err := JSReceive(receiverID, dataBytes)
// 	if err != nil {
// 		// Handle error if needed
// 		loginfo("Error in JSReceive:", err)
// 	}

//		return nil
//	}
func JSKeyGen(this js.Value, p []js.Value) interface{} {
	teleID := p[0].String()
	pk, err := KeyGen(teleID)
	if err != nil {
		// Handle error if needed
		loginfo("Error in JSReceive:", err)
	}

	return crypto.PubkeyToAddress(*pk)
}

func JSReceiveWrapper(this js.Value, p []js.Value) interface{} {
	receiverID := p[0].String()
	data := p[1]
	dataBytes := jsValueToBytes(data)

	err := JSReceive(receiverID, dataBytes)

	if err != nil {
		// Handle error if needed
		loginfo("Error in JSReceive:", err)
	}

	return nil
}

func jsValueToBytes(val js.Value) []byte {
	// Check if the js.Value is an instance of Uint8Array
	if val.InstanceOf(js.Global().Get("Uint8Array")) {
		// Prepare a byte slice of the same length
		byteSlice := make([]byte, val.Length())

		// Copy the data from the js.Value to the Go byte slice
		n := js.CopyBytesToGo(byteSlice, val)
		if n != val.Length() {
			panic(fmt.Errorf("failed to copy the entire buffer"))
			return nil //, fmt.Errorf("failed to copy the entire buffer")
		}

		return byteSlice
	}
	panic(fmt.Errorf("value is not a Uint8Array"))
	return nil //, fmt.Errorf("value is not a Uint8Array")
}
