# pglm 오픈소스

## 개요

`pglm`은 PostgreSQL의 `NOTIFY/LISTEN` 기능을 비동기적으로 처리할 수 있도록 설계된 Go 패키지입니다.

`pgx` 기반으로 개발되었으며, [pgln](https://github.com/xxx/pgln)의 구현 방식을 참고하여 커스텀 마이징했습니다.

인터벌 방식이나 flag 방식 대신 **Go 채널 기반 방식**을 사용하여 보다 효율적으로 프로세스 간 통신을 처리할 수 있도록 설계되었습니다.

## 개발 배경

[`autodata-manager`](https://github.com/Jaeun-Choi98/autodata-manager) 프로젝트를 진행하면서 PostgreSQL의 `NOTIFY/LISTEN` 기능을 간단히 구현하여 사용했으나,

다음과 같은 문제점이 있었습니다:

- **DB 연결이 끊겼을 때의 복구 문제**
- **채널별로 들어오는 데이터를 적절히 처리하지 못하는 문제**

이러한 문제를 보완하고, 패키지화하여 보다 안정적으로 관리할 수 있도록 `pglm`을 개발했습니다.

## 설치

```
go get github.com/Jaeun-Choi98/pglm
```

## 사용법

### `pglm` 객체 생성

`ListenerManagerBuilder.Build()`를 통해 `ListenManager` 객체를 생성할 수 있습니다.

```go
lm, err := pglm.NewListenManagerBuilder().
    SetConn("your_db_connection_info").  // DB 연결 정보 설정
    SetContext(context.Background()).    // 컨텍스트 설정
    SetReconnectInterval(1 * time.Second). // DB 재연결 간격 설정
    SetBlockCheckTimeout(5 * time.Second). // 블로킹 제한 시간 설정
    Build()
if err != nil {
    log.Fatalf("Failed to create ListenManager: %v", err)
}
```

### 주요 메서드

`StartListening()`: 수신을 시작 (non-blocking)

`Listen(channel string, handler ListenHandler)`: 특정 채널을 구독하고 핸들러(콜백 함수)를 설정

`Notify(channel string, payload string)`: 해당 채널에 메시지를 전송

`Unlisten(channel string)`: 특정 채널 구독 해제

`Shutdown()`: `ListenManager` 종료

### 주의 사항

- `Listen(channel string, handler ListenHandler)`에서 사용되는 핸들러의 콜백 함수는 내부적으로 **블로킹** 상태가 유지됩니다.
- 따라서, 개발자의 용도에 맞게 **비동기 처리 또는 Goroutine을 활용한 설계**가 필요합니다.

## 사용 예제

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/joho/godotenv"
	"github.com/Jaeun-Choi98/pglm"
)

func main() {
	// 환경 변수 로드
	if err := godotenv.Load(); err != nil {
		log.Fatal("Error loading .env file")
	}
	connInfo := os.Getenv("DB_CONN_INFO")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ListenManager 생성
	lm, err := pglm.NewListenManagerBuilder().
		SetConn(connInfo).
		SetContext(ctx).
		SetReconnectInterval(1 * time.Second).
		SetBlockCheckTimeout(5 * time.Second).
		Build()
	if err != nil {
		log.Fatalf("Failed to create ListenManager: %v", err)
	}

	// 리스너 시작
	if err := lm.StartListening(); err != nil {
		log.Fatalf("Failed to start listening: %v", err)
	}
	fmt.Println("Listening started...")

	var wg sync.WaitGroup
	check := make(chan bool)

	wg.Add(1)
	go func() {
		defer wg.Done()
		channelName := "example_channel"

		// 핸들러 등록
		handler := pglm.ListenHandler{
			HandleNotification: func(channel, payload string) {
				fmt.Printf("Received notification: Channel=%s, Payload=%s\\n", channel, payload)
				check <- true
			},
			HandleError: func(channel string, err error) {
				log.Printf("Error on channel %s: %v\\n", channel, err)
			},
		}

		if err := lm.Listen(channelName, handler); err != nil {
			log.Fatalf("Failed to listen to channel: %v", err)
		}
	}()

	wg.Wait()

	// 테스트용 NOTIFY 전송
	if err := lm.Notify("example_channel", "Hello, pglm!"); err != nil {
		log.Fatalf("Failed to send notification: %v", err)
	}

	// 핸들러 정상 동작 확인
	select {
	case <-check:
	case <-time.After(3 * time.Second):
		log.Fatalln("Failed to check the handler operation")
	}

	// Unlisten 및 Shutdown
	if err := lm.Unlisten("example_channel"); err != nil {
		log.Fatalf("Failed to unlisten: %v", err)
	}
	fmt.Println("Unlistened successfully.")

	if err := lm.Shutdown(); err != nil {
		log.Fatalf("Failed to shutdown: %v", err)
	}
	fmt.Println("Shutdown successfully.")
}

```

## 테스트

### 부하 테스트

- 100명의 클라이언트가 동시에 메시지를 수신하는 시뮬레이션 테스트를 진행하였습니다.
- 테스트 예시는 `pglm_test.go`에서 확인할 수 있습니다.

### 실행 방법

`go test -v`
