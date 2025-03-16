package main

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

func networkManager(ctx context.Context, wg *sync.WaitGroup) chan string {
	netChan := make(chan string)

	connChan := connMaker(ctx, wg)

	wg.Add(1)
	go func() {
		defer wg.Done()

		for {
			select {
			case <-ctx.Done():
				printToLog("Network shutting down")
				return

			case ip := <-netChan:
				wg.Add(1)
				go connectToPeer(ctx, wg, ip)

			case conn := <-connChan:
				wg.Add(1)
				go handleConnection(ctx, wg, conn)
			}
		}
	}()

	return netChan
}

func connMaker(ctx context.Context, wg *sync.WaitGroup) chan net.Conn {

	acceptChan := make(chan net.Conn)

	connChan := make(chan net.Conn)

	wg.Add(1)
	go func() {
		defer wg.Done()

		listener, err := net.Listen("tcp", ":8080")
		if err != nil {
			printToLog(fmt.Sprintf("Network error: %v", err))
			return
		}
		defer listener.Close()

		printToLog("Network listening on :8080")

		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				conn, err := listener.Accept()
				if err != nil {
					if errString := err.Error(); strings.Contains(errString, "use of closed network connection") {
						printToLog("Listener closed")
						return
					}
					printToLog(fmt.Sprintf("Accept error: %v", err))
					continue
				}
				acceptChan <- conn
			}
		}()

		for {
			select {
			case <-ctx.Done():
				printToLog("ConnMaker Shutting Down")
				return

			case conn := <-acceptChan:
				printToLog(fmt.Sprintf("Connected to peer: %s", conn.RemoteAddr()))
				connChan <- conn
			}
		}
	}()

	return connChan

}

func handleConnection(ctx context.Context, wg *sync.WaitGroup, conn net.Conn) {
	defer wg.Done()
	defer conn.Close()

	for {
		select {
		case <-ctx.Done():
			printToLog("Context canceled, closing connection")
			return
		default:
			time.Sleep(1 * time.Second)
		}
	}
	// Decode incoming block, send to blockchan
}

func connectToPeer(ctx context.Context, wg *sync.WaitGroup, ip string) {
	defer wg.Done()

	if !strings.Contains(ip, ":") {
		ip = ip + ":8080"
	}

	conn, err := net.DialTimeout("tcp", ip, 5*time.Second)
	if err != nil {
		printToLog(fmt.Sprintf("Failed to connect to %s: %v", ip, err))
		return
	}
	defer conn.Close()

	printToLog(fmt.Sprintf("Connected to peer: %s", ip))
	for {
		select {
		case <-ctx.Done():
			printToLog("Context canceled, closing connection")
			return
		default:
			time.Sleep(1 * time.Second)
		}
	}

}

func broadcastBlock(height int) {
	printToLog(fmt.Sprintf("Broadcasting Block %d to Pareme....", height))
}
