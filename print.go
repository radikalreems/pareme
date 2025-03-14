package main

import (
	"context"
	"os"
	"sync"
	"time"
)

var printChan chan string

func initPrinter(ctx context.Context, wg *sync.WaitGroup) {
	// delete existing pareme.log
	if _, err := os.Stat("pareme.log"); err == nil {
		os.Truncate("pareme.log", 0)
	}

	// Open log file in append mode
	f, err := os.OpenFile("pareme.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		panic("failed to open pareme.log: " + err.Error())
	}

	printChan = make(chan string, 100)

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer f.Close()
		defer close(printChan)

		for {
			select {
			case msg := <-printChan:
				if _, err := f.WriteString(msg + "\n"); err != nil {
					continue
				}
			case <-ctx.Done():
				// Drain remaining messages
				time.Sleep(2 * time.Second)
				for len(printChan) > 0 {
					if msg, ok := <-printChan; ok {
						f.WriteString(msg + "\n")
					}
				}
				f.WriteString("Printer is shutting down")
				return
			}
		}
	}()
}

func printToLog(s string) {
	select {
	case printChan <- s:
	case <-time.After(1 * time.Second):
	}
}
