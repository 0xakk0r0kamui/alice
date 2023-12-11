// Copyright © 2020 AMIS Technologies
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package message

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/getamis/alice/types"
	"github.com/getamis/sirius/log"
)

var (
	ErrOldMessage             = errors.New("old message")
	ErrBadMsg                 = errors.New("bad message")
	ErrInvalidStateTransition = errors.New("invalid state transition")
	ErrDupMsg                 = errors.New("duplicate message")
)

type MsgMain struct {
	logger         log.Logger
	peerNum        uint32
	msgChs         *MsgChans
	state          types.MainState
	currentHandler types.Handler
	listener       types.StateChangedListener

	lock        sync.RWMutex
	handlerLock sync.RWMutex
	cancel      context.CancelFunc
	id          string
}

func NewMsgMain(id string, peerNum uint32, listener types.StateChangedListener, initHandler types.Handler, msgTypes ...types.MessageType) *MsgMain {
	fmt.Printf("DEBUG Log %v\n", id)
	return &MsgMain{
		logger:         log.New("self", id),
		peerNum:        peerNum,
		msgChs:         NewMsgChans(peerNum, msgTypes...),
		state:          types.StateInit,
		currentHandler: initHandler,
		listener:       listener,
		id:             id,
	}
}

func (t *MsgMain) Start() {
	t.lock.Lock()
	defer t.lock.Unlock()

	if t.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	fmt.Println("AaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaA")
	//nolint:errcheck
	go t.messageLoop(ctx)
	t.cancel = cancel
}

func (t *MsgMain) Stop() {
	t.lock.Lock()
	defer t.lock.Unlock()

	if t.cancel == nil {
		return
	}
	t.cancel()
	t.cancel = nil
}

func (t *MsgMain) AddMessage(senderId string, msg types.Message) error {
	if senderId != msg.GetId() {
		t.logger.Debug("Different sender", "senderId", senderId, "msgId", msg.GetId())
		return ErrBadMsg
	}
	currentMsgType := t.GetHandler().MessageType()
	newMessageType := msg.GetMessageType()
	if currentMsgType > newMessageType {
		t.logger.Debug("Ignore old message", "currentMsgType", currentMsgType, "newMessageType", newMessageType)
		return ErrOldMessage
	}
	return t.msgChs.Push(msg)
}

func (t *MsgMain) GetHandler() types.Handler {
	t.handlerLock.RLock()
	defer t.handlerLock.RUnlock()

	return t.currentHandler
}

func (t *MsgMain) GetState() types.MainState {
	return t.state
}

func (t *MsgMain) messageLoop(ctx context.Context) (err error) {
	defer func() {
		panicErr := recover()

		if err == nil && panicErr == nil {
			fmt.Printf("DEBUG Log3\n")
			_ = t.setState(types.StateDone)
		} else {
			fmt.Printf("DEBUG Log4 %v %v\n", err, panicErr)
			_ = t.setState(types.StateFailed)
		}

		t.Stop()
	}()
	fmt.Println("AaaaaaaaaaaaasdjkfsdjfnkaaaaaaaaaaaaaaaaaaaaaaaaaaaaaA")
	handler := t.GetHandler()
	msgType := handler.MessageType()
	msgCount := uint32(0)
	// t.logger.id("test")
	for {
		fmt.Printf(" %v still check wait for %v \n", t.id, msgType)
		// 1. Pop messages
		// 2. Check if the message is handled before
		// 3. Handle the message
		// 4. Check if we collect enough messages
		// 5. If yes, finalize the handler. Otherwise, wait for the next message
		msg, err := t.msgChs.Pop(ctx, msgType)
		if err != nil {
			t.logger.Warn(t.id, "Failed to pop message", "err", err)
			fmt.Printf("DEBUG Log2\n")
			return err
		}
		fmt.Printf("Pop %v\n", msg.GetId())
		id := msg.GetId()
		logger := t.logger.New("msgType", msgType, "fromId", id)
		if handler.IsHandled(logger, id) {
			logger.Warn(t.id + "The message is handled before")
			continue
			return ErrDupMsg
		}

		err = handler.HandleMessage(logger, msg)
		if err != nil {
			logger.Warn(t.id+"Failed to save message", "err", err)
			fmt.Printf("DEBUG Log1\n")
			return err
		}

		msgCount++
		fmt.Printf("lplplplplp %v %v %v\n", t.id, msgCount, handler.GetRequiredMessageCount())
		if msgCount < handler.GetRequiredMessageCount() {
			continue
		}
		fmt.Printf("%v %v %v\n", t.id, handler, err)
		nextHandler, err := handler.Finalize(logger)
		fmt.Printf("%v %v %v\n", t.id, nextHandler, err)
		if err != nil {
			logger.Warn(t.id+" Failed to go to next handler", "err", err)
			return err
		}
		// if nextHandler is nil, it means we got the final result
		if nextHandler == nil {
			fmt.Printf("DEBUG Log\n")
			return nil
		}
		t.handlerLock.Lock()
		t.currentHandler = nextHandler
		handler = t.currentHandler
		t.handlerLock.Unlock()
		newType := handler.MessageType()
		logger.Info(t.id, "Change handler", "oldType", msgType, "newType", newType)
		msgType = newType
		msgCount = uint32(0)
	}
}

func (t *MsgMain) setState(newState types.MainState) error {
	if t.isInFinalState() {
		t.logger.Warn("Invalid state transition", "old", t.state, "new", newState)
		return ErrInvalidStateTransition
	}

	t.logger.Info("State changed", "old", t.state, "new", newState)
	oldState := t.state
	t.state = newState
	t.listener.OnStateChanged(oldState, newState)
	return nil
}

func (t *MsgMain) isInFinalState() bool {
	return t.state == types.StateFailed || t.state == types.StateDone
}
