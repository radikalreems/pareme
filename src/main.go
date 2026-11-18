package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"pareme/src/common"
	"pareme/src/inout"
	"pareme/src/mine"
	"pareme/src/network"
	"pareme/src/ui"
	"strings"
	"sync"
)

// Initializes and runs the Pareme blockchain node
func main() {
	fmt.Println("Starting Pareme...")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // Ensure canellation on exit
	var wg sync.WaitGroup

	// Initialize the printer for logging to .log file
	common.InitPrinter(ctx, &wg)

	// Chain files are open before any peer can ask for a block.
	heights := make(chan []int, 10)
	chain, err := inout.Open(common.BlocksDir, heights)
	if err != nil {
		common.PrintToLog(fmt.Sprintf("Sync failed: %v", err))
		cancel()
		wg.Wait()
		return
	}
	defer chain.Close()

	// Start the networking goroutine
	network.NetworkManager(ctx, &wg, chain)

	// Start the miner manager with accepted heights from the chain
	consoleMineChan := mine.MinerManager(ctx, &wg, chain, heights)

	fmt.Println("Program running in headless mode. Type 'ui' to launch the GUI, or 'exit' to quit.")

	reader := bufio.NewReader(os.Stdin)
	for {
		select {
		case <-ctx.Done():
			fmt.Println("Context canceled, exiting...")
			wg.Wait()
			return
		default:
			fmt.Print("Pareme> ")
			input, err := reader.ReadString('\n')
			if err != nil {
				common.PrintToLog(fmt.Sprintf("Input error: %v", err))
				break
			}

			input = strings.TrimSpace(input)

			enteredUI := false
			switch {
			case input == "ui" && !enteredUI:
				ui.RunUI(ctx, cancel, &wg, consoleMineChan, chain)
				enteredUI = true
			case input == "exit":
				cancel()
				wg.Wait()
				return
			default:
				fmt.Println("Unknown command")
			}
		}
	}

}
