package main

import (
	"fmt"
)

func broadcastBlock(height int) {
	printToLog(fmt.Sprintf("Broadcasting Block %d to Pareme....", height))
}
