package pglm

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type HandleNotificationType func(channel string, payload string)

type HandleErrorType func(channel string, err error)

type ListenHandler struct {
	channel            string
	HandleNotification HandleNotificationType
	HandleError        HandleErrorType
}

type ListenerManagerInterface interface {
	StartListening() error
	Listen(channel string, listenHandler ListenHandler) error
	Unlisten(channel string) error
	Notify(channel string, payload string) error
	Shutdown() error
}

type ListenerManager struct {
	uuid              uuid.UUID
	connInfo          string
	reconnectInterval time.Duration
	blockCheckTimeout time.Duration
	ListenHandlers    map[string]*ListenHandler // channel -> ListeneHandler
	ctx               context.Context
	ctxCancelFunc     context.CancelFunc
	isStart           bool
	reqListenChan     chan string
	reqUnlistenChan   chan string
	shutdownChan      chan bool
	wgsd              sync.WaitGroup
	mu                sync.RWMutex
}

func (lm *ListenerManager) Listen(channel string, listenHandler ListenHandler) error {
	if !lm.isStart {
		log.Println("musted call StartListening() before calling Listen()")
		return fmt.Errorf("musted call StartListening() before calling Listen()")
	}
	newListener := listenHandler
	newListener.channel = channel

	lm.mu.Lock()
	lm.ListenHandlers[channel] = &newListener
	lm.mu.Unlock()
	select {
	case lm.reqListenChan <- channel:
	case <-time.After(lm.blockCheckTimeout):
		log.Printf("block reqListenChan")
		return fmt.Errorf("block reqListenChan")
	}

	err := lm.Notify(lm.uuid.String(), fmt.Sprintf("make new channel %s", channel))
	if err != nil {
		return err
	}
	return nil
}

func (lm *ListenerManager) Unlisten(channel string) error {
	if !lm.isStart {
		log.Println("musted call StartListening() before calling Unlisten()")
		return fmt.Errorf("musted call StartListening() before calling Unlisten()")
	}
	lm.mu.Lock()
	delete(lm.ListenHandlers, channel)
	lm.mu.Unlock()
	select {
	case lm.reqUnlistenChan <- channel:
	case <-time.After(lm.blockCheckTimeout):
		log.Printf("block reqUnlistenChan")
		return fmt.Errorf("block reqUnlistenChan")
	}

	err := lm.Notify(lm.uuid.String(), fmt.Sprintf("delete channel %s", channel))
	if err != nil {
		return err
	}

	return nil
}

func (lm *ListenerManager) Notify(channel string, payload string) error {

	select {
	case <-lm.ctx.Done():
		return nil
	default:
	}

	conn, err := pgx.Connect(lm.ctx, lm.connInfo)
	if err != nil {
		log.Println("failed to connect pgdb in Notify()")
		return err
	}
	defer conn.Close(lm.ctx)
	_, err = conn.Exec(lm.ctx, "SELECT pg_notify($1, $2)", channel, payload)
	if err != nil {
		log.Printf("failed to notify(%s, %s)\n", channel, payload)
		return fmt.Errorf("failed to notify(%s, %s)", channel, payload)
	}
	return nil
}

func (lm *ListenerManager) StartListening() error {

	if lm.isStart {
		log.Println("already started")
		return fmt.Errorf("already started")
	}

	lm.isStart = true
	lm.wgsd.Add(1)
	go lm.listeningLoop()

	return nil
}

// for{ connect() -> initForNotificationAndRecovery() -> waitForNotification()}
func (lm *ListenerManager) listeningLoop() {

	initForNotificationAndRecovery := func(conn *pgx.Conn) error {

		// Listen(), Unlisten(), Shutdown()을 실행하는 과정에서 pgx.Conn.WaitForNotification() 대기를 해제하기 위한 채널
		_, err := conn.Exec(lm.ctx, fmt.Sprintf("LISTEN %s", pgx.Identifier{lm.uuid.String()}.Sanitize()))
		if err != nil {
			return fmt.Errorf("failed to listen on uuid")
		}

		lm.mu.RLock()
		for channel := range lm.ListenHandlers {
			_, err := conn.Exec(lm.ctx, fmt.Sprintf("LISTEN %s", pgx.Identifier{channel}.Sanitize()))
			if err != nil {
				return fmt.Errorf("failed to listen on %s", channel)
			}
		}
		lm.mu.RUnlock()
		return nil
	}

	waitForNotification := func(conn *pgx.Conn) error {
		// LISTEN 또는 UNLISTEN 실행 함수
		listenUnlisten := func(channel, cmd string) error {
			_, err := conn.Exec(lm.ctx, fmt.Sprintf("%s %s", cmd, pgx.Identifier{channel}.Sanitize()))
			if err != nil {
				log.Printf("failed to %s %s: %v", cmd, channel, err)
				return fmt.Errorf("failed to %s %s: %v", cmd, channel, err)
			}
			return nil
		}

		// 추가 요청을 연속으로 처리하는 함수
		processPendingRequests := func(requestChan chan string, cmd string) error {
			for {
				select {
				case pendingChannel := <-requestChan:
					if err := listenUnlisten(pendingChannel, cmd); err != nil {
						log.Printf("error handling %s for %s: %v", cmd, pendingChannel, err)
						return err
					}
				default:
					return nil
				}
			}
		}

		for {
			select {
			case reqChannel := <-lm.reqListenChan:
				lm.mu.Lock()
				if err := listenUnlisten(reqChannel, "LISTEN"); err != nil {
					lm.mu.Unlock()
					/*
						listenLoop은 정상 종료를 하지 않는 이상 계속 동작해야 하고,클라이언트에서는 Listen이 정상적으로 실행되었는지 알려주어야 함.
					*/
					//return err
				}
				if err := processPendingRequests(lm.reqListenChan, "LISTEN"); err != nil {
					lm.mu.Unlock()
					//return err
				}
				lm.mu.Unlock()
			case reqChannel := <-lm.reqUnlistenChan:
				lm.mu.Lock()
				if err := listenUnlisten(reqChannel, "UNLISTEN"); err != nil {
					lm.mu.Unlock()
					//return err
				}
				if err := processPendingRequests(lm.reqUnlistenChan, "UNLISTEN"); err != nil {
					lm.mu.Unlock()
					//return err
				}
				lm.mu.Unlock()
			case <-lm.shutdownChan:
				log.Println("Stop signal received. Exiting listen loop...")
				lm.mu.RLock()
				lm.ctxCancelFunc()
				lm.mu.RUnlock()
				//time.Sleep(3 * time.Second)
				lm.wgsd.Done()
				return nil
			case <-lm.ctx.Done():
				return nil

			default:
			}

			// PostgreSQL의 NOTIFY 이벤트 대기 및 처리
			notification, err := conn.WaitForNotification(lm.ctx)
			if err != nil {
				log.Println("failed to wait for notification")
				return err
			}

			// 핸들러가 있으면 실행
			lm.mu.RLock()
			if handler, exists := lm.ListenHandlers[notification.Channel]; exists && handler.HandleNotification != nil {
				handler.HandleNotification(notification.Channel, notification.Payload)
			}
			lm.mu.RUnlock()
		}
	}

	connect := func() error {
		select {
		case <-lm.ctx.Done():
			return nil
		default:
		}

		conn, err := pgx.Connect(lm.ctx, lm.connInfo)
		if err != nil {
			log.Println("failed to connect pgdb")
			return err
		}
		defer conn.Close(lm.ctx)
		err = initForNotificationAndRecovery(conn)
		if err != nil {
			return err
		}
		err = waitForNotification(conn)
		if err != nil {
			return err
		}
		return nil
	}

	for {

		err := connect()
		if err != nil {
			lm.mu.RLock()
			for channel, listenHandler := range lm.ListenHandlers {
				if listenHandler.HandleError != nil {
					listenHandler.HandleError(channel, err)
				}
			}
			lm.mu.RUnlock()
		}

		select {
		case <-lm.ctx.Done():
			return
		case <-time.After(lm.reconnectInterval):

		}
	}
}

func (lm *ListenerManager) Shutdown() error {
	lm.mu.RLock()
	if !lm.isStart {
		log.Println("not currently listening")
		return fmt.Errorf("not currently listening")
	}
	lm.mu.RUnlock()

	select {
	case lm.shutdownChan <- true:
	case <-time.After(lm.blockCheckTimeout):
		log.Printf("block stopFlagChan")
		return fmt.Errorf("block stopFlagChan")
	}

	err := lm.Notify(lm.uuid.String(), "shutdown")
	if err != nil {
		return err
	}

	lm.wgsd.Wait()
	lm.isStart = false
	close(lm.shutdownChan)
	close(lm.reqListenChan)
	close(lm.reqUnlistenChan)
	log.Println("shutdown succesfully!")
	return nil
}
