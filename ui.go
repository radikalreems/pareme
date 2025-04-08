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

// Global variable for output display
var outputText *widget.Label

func newSpacer() *fyne.Container {
	return container.NewVBox(
		widget.NewLabel(""),
		widget.NewSeparator(),
		widget.NewLabel(""),
	)
}

func runUI(ctx context.Context, cancel context.CancelFunc, wg *sync.WaitGroup, consoleMineChan chan string, dialIPChan chan string) {
	// Create Fyne UI
	a := app.New()
	w := a.NewWindow("Pareme Blockchain Node")
	w.Resize(fyne.NewSize(600, 400))

	// Create Output Text field for logging
	outputText = widget.NewLabel("Pareme Node Started\n")
	outputText.Wrapping = fyne.TextWrapWord

	// --------Network group--------
	type peerToggle struct {
		toggle *widget.Check
		id     int
		peer   *Peer
	}

	// Peer toggles for user to select peers
	var peerToggles = make(map[int]*peerToggle)
	var peerTogglesMutex sync.Mutex

	// Network group containers
	var networkGroup *fyne.Container
	peerListContainer := container.NewVBox()
	var peerControls *fyne.Container

	// IP entry field
	ipEntry := widget.NewEntry()
	ipEntry.SetPlaceHolder("Enter IP address")

	// Button to connect to given IP
	connectButton := widget.NewButton("Connect", func() {
		ip := ipEntry.Text
		if ip != "" {
			printToLog(fmt.Sprintf("Connecting to IP: %s", ip))
			dialIPChan <- ip
			outputText.SetText(outputText.Text + fmt.Sprintf("Connecting to %s\n", ip))
		}
	})

	// Create the baseline network group container
	networkGroup = container.NewVBox(
		widget.NewLabel("Network Controls"),
		ipEntry,
		connectButton,
	)

	// Ping button to send a ping to selected peer
	pingButton := widget.NewButton("Ping", func() {
		printToLog("Received 'ping' command")
		peerTogglesMutex.Lock()
		defer peerTogglesMutex.Unlock()

		selected := false
		for _, pt := range peerToggles {
			if pt.toggle.Checked {
				response := requestAMessage(pt.id, 0, nil)
				outputText.SetText(outputText.Text + describeMessageFrontEnd(response) + "\n")
				selected = true
			}
		}

		if !selected {
			outputText.SetText(outputText.Text + "No peers selected for ping\n")
		}

	})

	// Height button to request latest height from selected peer
	heightButton := widget.NewButton("Get Latest Height", func() {
		printToLog("Received 'height' command")
		peerTogglesMutex.Lock()
		defer peerTogglesMutex.Unlock()

		selected := false
		for _, pt := range peerToggles {
			if pt.toggle.Checked {
				response := requestAMessage(pt.id, 1, nil)
				outputText.SetText(outputText.Text + describeMessageFrontEnd(response) + "\n")
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

	// --------Mining group--------

	// Hash entry field
	hashEntry := widget.NewEntry()
	hashEntry.SetPlaceHolder("Enter Hash To Mine")
	hashEntry.Text = "78d031901879f64da61a41dd1e0c8a16d1ace1b9b61133db079413a816e765d2" // "pareme"

	// Button to start mining with given hash
	startMineButton := widget.NewButton("Start Mining", func() {
		printToLog("Recieved 'start mine' command")
		hash := hashEntry.Text
		consoleMineChan <- hash
		outputText.SetText(outputText.Text + "Mining started\n")
	})

	// Button to stop mining
	stopMineButton := widget.NewButton("Stop Mining", func() {
		printToLog("Recieved 'stop mine' command")
		consoleMineChan <- ""
		outputText.SetText(outputText.Text + "Mining stopped\n")
	})

	// Button to stop node and quit
	stopButton := widget.NewButton("Stop Node", func() {
		printToLog("Recieved 'stop' command")
		cancel()
		wg.Wait()
		outputText.SetText(outputText.Text + "Node stopped\n")
		a.Quit()
	})

	// Create the mining group container
	miningGroup := container.NewVBox(
		widget.NewLabel("Mining Controls"),
		hashEntry,
		container.NewHBox(startMineButton, stopMineButton),
	)

	// --------UI OVERLAY--------

	// Spacer to separate the groups

	// Large field for logging
	outputScroll := container.NewScroll(outputText)
	outputScroll.SetMinSize(fyne.NewSize(0, 200))

	// UI overlay
	content := container.NewVBox(
		widget.NewLabel("Pareme Blockchain Node"),
		networkGroup,
		newSpacer(),
		miningGroup,
		newSpacer(),
		stopButton,
		outputScroll,
	)

	// Run UI
	w.SetContent(content)
	updatePeerList()
	w.ShowAndRun()
}
