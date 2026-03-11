package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: testservice <http|sleep|fail>")
		os.Exit(2)
	}

	switch os.Args[1] {
	case "http":
		runHTTP(os.Args[2:])
	case "sleep":
		runSleep(os.Args[2:])
	case "fail":
		runFail(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown mode %s\n", os.Args[1])
		os.Exit(2)
	}
}

func runHTTP(args []string) {
	flags := flag.NewFlagSet("http", flag.ExitOnError)
	port := flags.Int("port", 0, "port")
	message := flags.String("message", "hello", "message")
	_ = flags.Parse(args)

	server := &http.Server{
		Addr: fmt.Sprintf("127.0.0.1:%d", *port),
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			_, _ = writer.Write([]byte(*message))
		}),
	}

	fmt.Printf("http_service_starting port=%d message=%s\n", *port, *message)
	done := make(chan error, 1)
	go func() {
		done <- server.ListenAndServe()
	}()

	signalCh := make(chan os.Signal, 2)
	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signalCh)

	select {
	case sig := <-signalCh:
		fmt.Printf("http_service_stopping signal=%s\n", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	case err := <-done:
		if err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "http_service_failed error=%v\n", err)
			os.Exit(1)
		}
	}
}

func runSleep(args []string) {
	flags := flag.NewFlagSet("sleep", flag.ExitOnError)
	duration := flags.Duration("duration", time.Minute, "duration")
	message := flags.String("message", "worker", "message")
	_ = flags.Parse(args)

	fmt.Printf("sleep_service_started message=%s\n", *message)
	timer := time.NewTimer(*duration)
	defer timer.Stop()

	signalCh := make(chan os.Signal, 2)
	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signalCh)

	select {
	case sig := <-signalCh:
		fmt.Printf("sleep_service_stopping signal=%s\n", sig)
	case <-timer.C:
		fmt.Println("sleep_service_done")
	}
}

func runFail(args []string) {
	flags := flag.NewFlagSet("fail", flag.ExitOnError)
	code := flags.Int("code", 7, "exit code")
	message := flags.String("message", "boom", "message")
	_ = flags.Parse(args)

	fmt.Fprintf(os.Stderr, "fail_service_exiting code=%d message=%s\n", *code, *message)
	time.Sleep(200 * time.Millisecond)
	os.Exit(*code)
}
