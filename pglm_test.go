package pglm_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Jaeun-Choi98/pglm"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListenerManager(t *testing.T) {
	godotenv.Load()
	testConnInfo := os.Getenv("DB_CONN_INFO_TEST")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lm, err := pglm.NewListenManagerBuilder().
		SetConn(testConnInfo).
		SetContext(ctx).
		SetReconnectInterval(1 * time.Second).
		SetBlockCheckTimeout(5 * time.Second).
		Build()
	require.NoError(t, err)

	err = lm.StartListening()
	require.NoError(t, err)
	t.Log("Started listening")

	notificationReceived := make(chan bool)
	channelName := "test_channel"
	var wg sync.WaitGroup
	wg.Add(1)
	// 다중 클라이언트 처리
	go func() {
		defer wg.Done()
		handler := pglm.ListenHandler{
			HandleNotification: func(channel, payload string) {
				notificationReceived <- true
				t.Logf("Received notification on channel: %s, payload: %s", channel, payload)
			},
			HandleError: func(channel string, err error) {
				t.Errorf("Error on channel %s: %v", channel, err)
			},
		}
		err = lm.Listen(channelName, handler)
		require.NoError(t, err)
	}()
	wg.Wait()

	err = lm.Notify(channelName, "test_payload")
	require.NoError(t, err)

	assert.True(t, <-notificationReceived, "Expected to receive a notification, but didn't")

	err = lm.Unlisten(channelName)
	require.NoError(t, err)
	t.Log("Unlistened successfully")

	err = lm.Shutdown()
	require.NoError(t, err)
	t.Log("Shutdown successfully")
}

// 100 client
func TestStressListenerManager(t *testing.T) {
	godotenv.Load()
	testConnInfo := os.Getenv("DB_CONN_INFO_TEST")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lm, err := pglm.NewListenManagerBuilder().
		SetConn(testConnInfo).
		SetContext(ctx).
		SetReconnectInterval(1 * time.Second).
		SetBlockCheckTimeout(5 * time.Second).
		Build()
	require.NoError(t, err)

	err = lm.StartListening()
	require.NoError(t, err)
	t.Log("Started listening")

	// 알림 수신 채널 맵 생성
	notificationReceivedChans := make(map[string]chan bool)
	var mu sync.Mutex

	for i := 0; i < 100; i++ {
		channelName := fmt.Sprintf("test_channel_%d", i)
		notificationReceivedChans[channelName] = make(chan bool, 1) // 버퍼 1 추가 (데드락 방지)
	}

	var wg sync.WaitGroup

	// 클라이언트 고루틴 함수
	client := func(channelName string) {
		defer wg.Done()
		handler := pglm.ListenHandler{
			HandleNotification: func(channel, payload string) {
				mu.Lock()
				if ch, exists := notificationReceivedChans[channel]; exists {
					select {
					case ch <- true:
					default:
					}
				}
				mu.Unlock()
				t.Logf("Received notification on channel: %s, payload: %s", channel, payload)
			},
			HandleError: func(channel string, err error) {
				t.Errorf("Error on channel %s: %v", channel, err)
			},
		}
		err := lm.Listen(channelName, handler)
		require.NoError(t, err)
	}

	// 100개의 리스너 실행
	wg.Add(100)
	for i := 0; i < 100; i++ {
		go client(fmt.Sprintf("test_channel_%d", i))
	}
	wg.Wait()

	// 100개 채널에 알림 전송
	for i := 0; i < 100; i++ {
		err := lm.Notify(fmt.Sprintf("test_channel_%d", i), fmt.Sprintf("test_payload_%d", i))
		require.NoError(t, err)
	}

	// 타임아웃을 적용한 알림 수신 대기 함수
	waitForNotificationWithTimeout := func(timeout time.Duration) bool {
		cnt := 0
		done := make(chan struct{})

		go func() {
			for cnt < 100 {
				for i := 0; i < 100; i++ {
					mu.Lock()
					ch, exists := notificationReceivedChans[fmt.Sprintf("test_channel_%d", i)]
					mu.Unlock()
					if !exists {
						continue
					}

					select {
					case <-ch:
						cnt++
					default:
					}
				}
			}
			close(done)
		}()

		select {
		case <-done:
			return true
		case <-time.After(timeout):
			return false
		}
	}

	// 10초 안에 모든 알림 수신 확인
	ret := waitForNotificationWithTimeout(10 * time.Second)
	assert.True(t, ret, "Expected to receive true value")

	// 구독 해제
	for i := 0; i < 100; i++ {
		err := lm.Unlisten(fmt.Sprintf("test_channel_%d", i))
		require.NoError(t, err)
	}

	// 리스너 매니저 종료
	err = lm.Shutdown()
	require.NoError(t, err)
	t.Log("Shutdown successfully")
}
