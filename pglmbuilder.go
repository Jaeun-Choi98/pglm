package pglm

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type ListenerManagerBuilder interface {
	SetConn(connInfo string) ListenerManagerBuilder
	SetContext(ctx context.Context) ListenerManagerBuilder
	SetReconnectInterval(reconnectInterval time.Duration) ListenerManagerBuilder
	SetBlockCheckTimeout(blockCheckTimeout time.Duration) ListenerManagerBuilder
	Build() (ListenerManagerInterface, error)
}

type listenerManagerBuilder struct {
	lm  *ListenerManager
	err error
}

func (lmb *listenerManagerBuilder) SetConn(connInfo string) ListenerManagerBuilder {
	if lmb.lm.connInfo != "" {
		return lmb
	}
	lmb.lm.connInfo = connInfo
	return lmb
}

func (lmb *listenerManagerBuilder) SetContext(ctx context.Context) ListenerManagerBuilder {
	if lmb.lm.ctx != nil {
		return lmb
	}
	lmb.lm.ctx = ctx
	return lmb
}

func (lmb *listenerManagerBuilder) SetReconnectInterval(reconnectInterval time.Duration) ListenerManagerBuilder {
	if lmb.err != nil {
		return lmb
	}
	if reconnectInterval <= 0 {
		lmb.err = fmt.Errorf("invaild reconnectInterval")
	}
	lmb.lm.reconnectInterval = reconnectInterval
	return lmb
}

func (lmb *listenerManagerBuilder) SetBlockCheckTimeout(blockCheckTimeout time.Duration) ListenerManagerBuilder {
	if lmb.err != nil {
		return lmb
	}
	if blockCheckTimeout <= 0 {
		lmb.err = fmt.Errorf("invaild pingCheckTimeout")
	}
	lmb.lm.blockCheckTimeout = blockCheckTimeout
	return lmb
}

func (lmb *listenerManagerBuilder) Build() (ListenerManagerInterface, error) {
	if lmb.err != nil {
		return nil, lmb.err
	}
	if lmb.lm.connInfo == "" {
		return nil, fmt.Errorf("connInfo is empty")
	}
	if lmb.lm.ctx == nil {
		return nil, fmt.Errorf("context is null")
	}
	if lmb.lm.reconnectInterval == 0 {
		lmb.lm.reconnectInterval = 1 * time.Second
	}
	if lmb.lm.blockCheckTimeout == 0 {
		lmb.lm.blockCheckTimeout = 5 * time.Second
	}
	ctx, cancel := context.WithCancel(lmb.lm.ctx)
	lmb.lm.ctx = ctx
	lmb.lm.ctxCancelFunc = cancel
	lmb.lm.uuid = uuid.New()
	lmb.lm.ListenHandlers = make(map[string]*ListenHandler)
	lmb.lm.shutdownChan = make(chan bool, 1)
	lmb.lm.reqListenChan = make(chan string, 10)
	lmb.lm.reqUnlistenChan = make(chan string, 10)
	return lmb.lm, nil
}

func NewListenManagerBuilder() ListenerManagerBuilder {
	return &listenerManagerBuilder{
		lm: &ListenerManager{},
	}
}
