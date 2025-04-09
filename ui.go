package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// PeerToggle defines the structure for a peer selection checkbox in the UI
type PeerToggle struct {
	toggle *widget.Check
	id     int
	peer   *Peer
}

// Global outputText displays log messages in the UI
var outputText *widget.Label

// newSpacer creates a separator for UI grouping
func newSpacer() *fyne.Container {
	return container.NewVBox(
		widget.NewLabel(""),
		widget.NewSeparator(),
		widget.NewLabel(""),
	)
}

func createTitleLabel(text string) *canvas.Text {
	title := canvas.NewText(text, theme.ForegroundColor())
	title.TextSize = 18
	title.TextStyle = fyne.TextStyle{Bold: true}
	title.Alignment = fyne.TextAlignCenter
	return title
}

// createNetworkGroup builds the network control section
func createNetworkGroup(dialIPChan chan string) *fyne.Container {
	// IP entry field
	ipEntry := widget.NewEntry()
	ipEntry.SetPlaceHolder("Enter IP address")

	// Button to connect to given IP
	connectButton := widget.NewButton("Connect", func() {
		if ip := ipEntry.Text; ip != "" {
			printToLog(fmt.Sprintf("Connecting to IP: %s", ip))
			dialIPChan <- ip
			outputText.SetText(outputText.Text + fmt.Sprintf("Connecting to %s\n", ip))
		}
	})

	// Create the baseline network group container
	return container.NewVBox(
		createTitleLabel("Network Controls"),
		container.NewPadded(ipEntry),
		container.NewPadded(connectButton),
		layout.NewSpacer(),
	)
}

// createPeerControls constructs the peer interaction section
func createPeerControls(peerToggles map[int]*PeerToggle, mutex *sync.Mutex) (*fyne.Container, *fyne.Container, *widget.Button, *widget.Button) {
	peerListContainer := container.NewVBox()

	// Ping button to send a ping to selected peer
	pingButton := widget.NewButton("Ping", func() {
		printToLog("Received 'ping' command")
		mutex.Lock()
		defer mutex.Unlock()

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
	pingButton.Disable()

	// Height button to request latest height from selected peer
	heightButton := widget.NewButton("Get Latest Height", func() {
		printToLog("Received 'height' command")
		mutex.Lock()
		defer mutex.Unlock()

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
	heightButton.Disable()

	peerControls := container.NewVBox(
		createTitleLabel("Connected Peers"),
		container.NewPadded(container.NewVScroll(peerListContainer)),
		container.NewHBox(
			container.NewPadded(pingButton),
			container.NewPadded(heightButton),
		),
		layout.NewSpacer(),
	)

	return peerControls, peerListContainer, pingButton, heightButton

}

// createMiningGroup sets up the mining control section
func createMiningGroup(consoleMineChan chan string) *fyne.Container {
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
	startMineButton.Importance = widget.HighImportance

	// Button to stop mining
	stopMineButton := widget.NewButton("Stop Mining", func() {
		printToLog("Recieved 'stop mine' command")
		consoleMineChan <- ""
		outputText.SetText(outputText.Text + "Mining stopped\n")
	})

	return container.NewVBox(
		createTitleLabel("Mining Controls"),
		container.NewPadded(hashEntry),
		container.NewHBox(
			container.NewPadded(startMineButton),
			container.NewPadded(stopMineButton),
		),
		layout.NewSpacer(),
	)
}

// updatePeerList synchronizes the UI peer list with the current set of connected peers
func updatePeerList(peerToggles map[int]*PeerToggle, mutex *sync.Mutex, container *fyne.Container, pingButton *widget.Button, heightButton *widget.Button) {
	mutex.Lock()
	defer mutex.Unlock()

	currentPeers := make(map[int]bool)
	for id := range AllPeers {
		printToLog(fmt.Sprintf("length of AllPeers: %v", len(AllPeers)))
		currentPeers[id] = true

		_, exists := peerToggles[id]
		printToLog(fmt.Sprintf("does id %v exist?: %v", id, exists))
		if !exists {
			printToLog("reached3")
			peer := AllPeers[id]
			toggle := widget.NewCheck(fmt.Sprintf("Peer %d: %v", id, peer.Address), nil)
			peerToggles[id] = &PeerToggle{
				toggle: toggle,
				id:     id,
				peer:   peer,
			}
			container.Add(toggle)
		}
	}
	// Remove peers that no longer exist
	for id := range peerToggles {
		if _, exists := currentPeers[id]; !exists {
			container.Remove(peerToggles[id].toggle)
			delete(peerToggles, id)
		}
	}
	container.Refresh()

	// Enable or disable buttons based on peer count
	if len(AllPeers) > 0 {
		pingButton.Enable()
		heightButton.Enable()
	} else {
		pingButton.Disable()
		heightButton.Disable()
	}
}

// runUI initializes and runs the main UI window with all components
func runUI(ctx context.Context, cancel context.CancelFunc, wg *sync.WaitGroup, consoleMineChan chan string, dialIPChan chan string) {
	// Create Fyne UI
	a := app.New()
	w := a.NewWindow("Pareme Blockchain Node")
	w.Resize(fyne.NewSize(600, 400))

	// Create Output Text field for logging
	outputText = widget.NewLabel("Pareme Node Started\n")
	outputText.Wrapping = fyne.TextWrapWord

	var peerToggles = make(map[int]*PeerToggle)
	var peerTogglesMutex sync.Mutex
	networkGroup := createNetworkGroup(dialIPChan)
	miningGroup := createMiningGroup(consoleMineChan)

	peerControls, peerListContainer, pingButton, heightButton := createPeerControls(peerToggles, &peerTogglesMutex)
	networkGroup.Add(peerControls)

	// Start peer list updater
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				updatePeerList(peerToggles, &peerTogglesMutex, peerListContainer, pingButton, heightButton)
			}
		}
	}()

	// Button to stop node and quit
	stopButton := widget.NewButton("Stop Node", func() {
		printToLog("Recieved 'stop' command")
		cancel()
		wg.Wait()
		outputText.SetText(outputText.Text + "Node stopped\n")
		a.Quit()
	})
	stopButton.Importance = widget.WarningImportance

	// --------UI OVERLAY--------
	outputScroll := container.NewScroll(outputText)
	outputScroll.SetMinSize(fyne.NewSize(0, 200))

	// Main title
	mainTitle := createTitleLabel("Pareme Blockchain Node")
	mainTitle.TextSize = 24

	// UI overlay
	content := container.NewVBox(
		container.NewPadded(mainTitle),
		networkGroup,
		newSpacer(),
		miningGroup,
		newSpacer(),
		stopButton,
		outputScroll,
	)

	// Run UI
	w.SetContent(content)
	updatePeerList(peerToggles, &peerTogglesMutex, peerListContainer, pingButton, heightButton)
	w.ShowAndRun()
}
