package main

import (
	"os"
)

var printChan chan string

func initPrinter() {
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

	go func() {
		defer f.Close()
		for msg := range printChan {
			if _, err := f.WriteString(msg + "\n"); err != nil {
				continue
			}
		}
	}()
}

func printToLog(s string) {
	printChan <- s
}
