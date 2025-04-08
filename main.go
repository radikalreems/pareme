package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// Global channels for communication between components
var blockChan = make(chan []Block, 100)             // Send a Block to have it verified and written to file
var requestChan = make(chan readRequest)            // Send a readRequest to retrieve a specific block
var indexRequestChan = make(chan chan [2]uint32, 1) // Send a channel to receive index info in it

// Global variable for output display
var outputText *widget.Label

// Initializes and runs the Pareme blockchain node
func main() {
	fmt.Println("Starting Pareme...")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // Ensure canellation on exit
	var wg sync.WaitGroup

	// Initialize the printer for logging to .log file
	initPrinter(ctx, &wg)

	// Start the networking goroutine
	dialIPChan := networkManager(ctx, &wg)

	// Sync chain data from own files
	err := syncChain()
	if err != nil {
		printToLog(fmt.Sprintf("Sync failed: %v", err))
		cancel()
		wg.Wait()
		return
	}

	// Chain is valid; start the block writer goroutine
	newBlockChan, err := blockWriter(ctx, &wg)
	if err != nil {
		printToLog(fmt.Sprintf("Blockwriter failed: %v", err))
		cancel()
		wg.Wait()
		return
	}

	/*
		// Connect to a peer
		dialIPChan <- "192.168.86.98"
		time.Sleep(2 * time.Second)

		// Sync chain data from peers
		err = syncToPeers()
		if err != nil {
			printToLog(fmt.Sprintf("Syncing chain from peers failed: %v", err))
			cancel()
			wg.Wait()
			return
		}
	*/

	// Start the miner manager with the current chain height
	consoleMineChan := minerManager(ctx, &wg, newBlockChan)

	// Create Fyne UI
	a := app.New()
	w := a.NewWindow("Pareme Blockchain Node")
	w.Resize(fyne.NewSize(600, 400))

	// Create UI components
	outputText = widget.NewLabel("Pareme Node Started\n")
	outputText.Wrapping = fyne.TextWrapWord
	outputScroll := container.NewScroll(outputText)
	outputScroll.SetMinSize(fyne.NewSize(0, 200))

	ipEntry := widget.NewEntry()
	ipEntry.SetPlaceHolder("Enter IP address")

	hashEntry := widget.NewEntry()
	hashEntry.SetPlaceHolder("Enter Hash To Mine")
	hashEntry.Text = "78d031901879f64da61a41dd1e0c8a16d1ace1b9b61133db079413a816e765d2" // "pareme"

	type peerToggle struct {
		toggle *widget.Check
		id     int
		peer   *Peer
	}

	var peerToggles = make(map[int]*peerToggle)
	var peerTogglesMutex sync.Mutex

	var networkGroup *fyne.Container
	peerListContainer := container.NewVBox()
	var peerControls *fyne.Container

	pingButton := widget.NewButton("Ping", func() {
		printToLog("Received 'ping' command")
		peerTogglesMutex.Lock()
		defer peerTogglesMutex.Unlock()

		selected := false
		for _, pt := range peerToggles {
			if pt.toggle.Checked {
				response := requestAMessage(pt.id, 0, nil)
				outputText.SetText(outputText.Text + describeMessage(response) + "\n")
				selected = true
			}
		}

		if !selected {
			outputText.SetText(outputText.Text + "No peers selected for ping\n")
		}

	})

	heightButton := widget.NewButton("Get Height", func() {
		printToLog("Received 'height' command")
		peerTogglesMutex.Lock()
		defer peerTogglesMutex.Unlock()

		selected := false
		for _, pt := range peerToggles {
			if pt.toggle.Checked {
				response := requestAMessage(pt.id, 1, nil)
				outputText.SetText(outputText.Text + describeMessage(response) + "\n")
				selected = true
			}
		}
		if !selected {
			outputText.SetText(outputText.Text + "No peers selected for height\n")
		}
	})
	// Function to update peer list
	updatePeerList := func() {
		peerTogglesMutex.Lock()
		defer peerTogglesMutex.Unlock()

		// Create a map of current peers
		currentPeers := make(map[int]bool)
		for id := range AllPeers {
			currentPeers[id] = true
			// Add new peers
			if _, exists := peerToggles[id]; !exists {
				peer := AllPeers[id]
				toggle := widget.NewCheck(fmt.Sprintf("Peer %d: %v", id, peer.Address), nil)
				peerToggles[id] = &peerToggle{
					toggle: toggle,
					id:     id,
					peer:   peer,
				}
				peerListContainer.Add(toggle)
			}
		}

		// Remove peers that no longer exist
		for id := range peerToggles {
			if _, exists := currentPeers[id]; !exists {
				peerListContainer.Remove(peerToggles[id].toggle)
				delete(peerToggles, id)
			}
		}
		peerListContainer.Refresh()

		hasPeers := len(AllPeers) > 0
		if hasPeers && peerControls == nil {
			// Create peer controls when first peer connects
			peerControls = container.NewVBox(
				widget.NewLabel("Connected Peers:"),
				container.NewHScroll(peerListContainer),
				container.NewHBox(pingButton, heightButton),
			)
			networkGroup.Add(peerControls)
		} else if !hasPeers && peerControls != nil {
			// Remove peer controls when no peers remain
			networkGroup.Remove(peerControls)
			peerControls = nil
		}
		networkGroup.Refresh()
	}

	// Start peer list updater
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				updatePeerList()
			}
		}
	}()

	// Network group
	connectButton := widget.NewButton("Connect", func() {
		ip := ipEntry.Text
		if ip != "" {
			printToLog(fmt.Sprintf("Connecting to IP: %s", ip))
			dialIPChan <- ip
			outputText.SetText(outputText.Text + fmt.Sprintf("Connecting to %s\n", ip))
		}
	})

	// Mining group
	startMineButton := widget.NewButton("Start Mining", func() {
		printToLog("Recieved 'start mine' command")
		hash := hashEntry.Text
		consoleMineChan <- hash
		outputText.SetText(outputText.Text + "Mining started\n")
	})

	stopMineButton := widget.NewButton("Stop Mining", func() {
		printToLog("Recieved 'stop mine' command")
		consoleMineChan <- ""
		outputText.SetText(outputText.Text + "Mining stopped\n")
	})

	// Stop node
	stopButton := widget.NewButton("Stop Node", func() {
		printToLog("Recieved 'stop' command")
		cancel()
		wg.Wait()
		outputText.SetText(outputText.Text + "Node stopped\n")
		a.Quit()
	})

	// Create grouped layouts
	networkGroup = container.NewVBox(
		widget.NewLabel("Network Controls"),
		ipEntry,
		connectButton,
	)

	miningGroup := container.NewVBox(
		widget.NewLabel("Mining Controls"),
		hashEntry,
		container.NewHBox(startMineButton, stopMineButton),
	)

	paddedNetworkGroup := container.NewPadded(networkGroup)
	paddedMiningGroup := container.NewPadded(miningGroup)
	paddedStopButton := container.NewPadded(stopButton)

	//spacer := widget.NewLabel("")
	//spacer.Resize(fyne.NewSize(0, 20))

	content := container.NewVBox(
		widget.NewLabel("Pareme Blockchain Node"),
		paddedNetworkGroup,
		//spacer,
		paddedMiningGroup,
		//spacer,
		paddedStopButton,
		outputScroll,
	)

	w.SetContent(content)
	updatePeerList()
	w.ShowAndRun()

	/*
		// Console command loop
		reader := bufio.NewReader(os.Stdin)
		for {
			fmt.Print("Pareme> ")
			input, err := reader.ReadString('\n')
			if err != nil {
				printToLog(fmt.Sprintf("Input error: %v", err))
				break
			}
			input = strings.TrimSpace(input)

			switch {
			case input == "stop":
				printToLog("Received 'stop' command")
				cancel()  // Signal all goroutines to stop
				wg.Wait() // Wait for all goroutines to finish
				fmt.Println("Stopping Pareme...")
				return
			case input == "start mine":
				printToLog("Received 'start mine' command")
				consoleMineChan <- 1
			case input == "stop mine":
				printToLog("Received 'stop mine' command")
				consoleMineChan <- 0
			case len(input) >= 10 && input[0:10] == "connect to":
				if len(input) <= 11 {
					fmt.Println("Please provide an IP address after 'connect to'")
					continue
				}
				ip := strings.TrimSpace(input[11:])
				printToLog(fmt.Sprintf("Connecting to IP: %s", ip))
				dialIPChan <- ip
			case input == "ping":
				printToLog("Received 'ping' command")
				var peerChoice int             // Which peer to ask
				for pos, _ := range AllPeers { // Pick random peer
					peerChoice = pos
					break
				}
				response := requestAMessage(peerChoice, 0, nil) // Ping | Payload:nil
				println(describeMessage(response))
			case input == "height":
				printToLog("Received 'height' command")
				var peerChoice int             // Which peer to ask
				for pos, _ := range AllPeers { // Pick random peer
					peerChoice = pos
					break
				}
				response := requestAMessage(peerChoice, 1, nil) // Height | Payload:nil
				println(describeMessage(response))
			case input[0:7] == "request":
				if len(input) <= 8 {
					fmt.Println("Please provide a range after 'request'")
					continue
				}
				heightRange := strings.TrimSpace(input[8:])
				parts := strings.Split(heightRange, "-")
				result := make([]byte, 8)
				num1, _ := strconv.ParseUint(parts[0], 10, 32)
				num2, _ := strconv.ParseUint(parts[1], 10, 32)
				binary.BigEndian.PutUint32(result[0:4], uint32(num1))
				binary.BigEndian.PutUint32(result[4:8], uint32(num2))
				var peerChoice int             // Which peer to ask
				for pos, _ := range AllPeers { // Pick random peer
					peerChoice = pos
					break
				}
				response := requestAMessage(peerChoice, 2, result) // Height | Payload:nil
				println(describeMessage(response))
			default:
				fmt.Println("Unknown command. Try 'stop'")
			}
		}
	*/
}
