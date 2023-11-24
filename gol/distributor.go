package gol

import (
	"fmt"
	"log"
	"net/rpc"
	"time"
	"uk.ac.bris.cs/gameoflife/stubs"
	"uk.ac.bris.cs/gameoflife/util"
)

type distributorChannels struct {
	events        chan<- Event
	ioCommand     chan<- ioCommand
	ioIdle        <-chan bool
	ioFilename    chan<- string
	ioOutput      chan<- uint8
	ioInput       <-chan uint8
	sdlKeyPresses <-chan rune
}

// distributor divides the work between workers and interacts with other goroutines.
func distributor(p Params, c distributorChannels) {
	// TODO: Create a 2D slice to store the world.
	world := loadPGM(p, c)

	// TODO: Execute all turns of the Game of Life.

	if p.Turns > 0 {
		// Connect to the broker
		broker, e := rpc.Dial("tcp", "localhost:8030")
		handleError(e)
		defer broker.Close()

		// Create request to send in rpc call
		request := stubs.Request{
			MessageWorld:   world,
			MessageHeight:  p.ImageHeight,
			MessageTurns:   p.Turns,
			MessageThreads: p.Threads}
		// Create response to store rpc result
		response := stubs.Response{MessageWorld: world}

		// Create channels to signal game finish and close AliveCells timer
		gameFinished := make(chan bool)
		aliveCellsFinished := make(chan bool)
		sdlFinished := make(chan bool)

		// Bool variable to pause and play AliveCells timer
		clockPaused := false

		// Run goroutines
		go play(broker, request, &response, gameFinished)
		//go sdlView(world, broker, c, p, sdlFinished)
		go processKeyPress(&clockPaused, broker, p, c)
		go getAliveCells(&clockPaused, broker, c, aliveCellsFinished)

		// Wait for game to finish
		<-gameFinished
		close(aliveCellsFinished)
		close(sdlFinished)

		// Update the world in the distributor
		world = response.MessageWorld
	}

	// TODO: Report the final state using FinalTurnCompleteEvent.
	finalAliveCells := make([]util.Cell, 0)
	for y := 0; y < p.ImageHeight; y++ {
		for x := 0; x < p.ImageWidth; x++ {
			if world[y][x] == 255 {
				finalAliveCells = append(finalAliveCells, util.Cell{X: x, Y: y})
			}
		}
	}

	finalEvent := FinalTurnComplete{
		CompletedTurns: p.Turns,
		Alive:          finalAliveCells,
	}

	c.events <- finalEvent

	// Save state of the board
	savePGM(p, c, world, p.Turns)

	// Make sure that the Io has finished any output before exiting.
	c.ioCommand <- ioCheckIdle
	<-c.ioIdle

	c.events <- StateChange{p.Turns, Quitting}

	// Close the channel to stop the SDL goroutine gracefully. Removing may cause deadlock.
	close(c.events)
}

func handleError(e error) {
	if e != nil {
		log.Fatal("Controller connection error: ", e)
	}
}

func processKeyPress(paused *bool, broker *rpc.Client, p Params, c distributorChannels) {
	response := stubs.Response{}
	for {
		keyPressed := <-c.sdlKeyPresses
		switch keyPressed {
		case 'k':
			*paused = true
			fmt.Println("Shutting down all distributed components...")

			//e := broker.Call("Broker.GetCurrentWorld", stubs.Request{}, &response)
			//handleError(e)
			//e = broker.Call("GetAliveCells", stubs.Request{}, &response)
			//handleError(e)
			e := broker.Call("Broker.Kill", stubs.Request{}, &(stubs.Response{}))
			handleError(e)
			return

		case 's':
			fmt.Println("Saving...")

			e := broker.Call("Broker.GetCurrentWorld", stubs.Request{}, &response)
			handleError(e)
			e = broker.Call("Broker.GetAliveCells", stubs.Request{}, &response)
			handleError(e)
			savePGM(p, c, response.MessageWorld, response.MessageTurns)

		case 'p':
			//e := broker.Call("Broker.GetCurrentWorld", stubs.Request{}, &response)
			//handleError(e)
			e := broker.Call("Broker.GetAliveCells", stubs.Request{}, &response)
			handleError(e)
			e = broker.Call("Broker.TogglePaused", stubs.Request{}, &(stubs.Response{}))
			handleError(e)

			if !*paused {
				c.events <- StateChange{response.MessageTurns, Paused}
			} else {
				c.events <- StateChange{response.MessageTurns, Executing}
			}
			*paused = !*paused

		case 'q':
			*paused = true
			fmt.Println("Shutting down controller...")

			e := broker.Call("Broker.GetCurrentWorld", stubs.Request{}, &response)
			handleError(e)
			e = broker.Call("Broker.GetAliveCells", stubs.Request{}, &response)
			handleError(e)
			savePGM(p, c, response.MessageWorld, response.MessageTurns)

			// Make sure that the Io has finished any output before exiting.
			c.ioCommand <- ioCheckIdle
			<-c.ioIdle

			c.events <- StateChange{response.MessageTurns, Quitting}

			// Close the channel to stop the SDL goroutine gracefully. Removing may cause deadlock.
			close(c.events)
		}
	}

}

func loadPGM(p Params, c distributorChannels) [][]byte {
	world := make([][]byte, p.ImageHeight)
	for i := range world {
		world[i] = make([]byte, p.ImageWidth)
	}

	// Read file by sending fileName to io.go
	// Gives command to read pgm file
	c.ioCommand <- ioInput
	c.ioFilename <- fmt.Sprintf("%dx%d", p.ImageHeight, p.ImageWidth)

	for y := 0; y < p.ImageHeight; y++ {
		for x := 0; x < p.ImageWidth; x++ {
			// bytes are recieved from io.go to fill in the board
			cellValue := <-c.ioInput
			if cellValue == 255 {
				c.events <- CellFlipped{CompletedTurns: 0, Cell: util.Cell{X: x, Y: y}}
			}
			world[y][x] = cellValue
		}
	}
	return world
}

func savePGM(p Params, c distributorChannels, world [][]byte, turn int) {
	// Save state of the board
	c.ioCommand <- ioOutput
	c.ioFilename <- fmt.Sprintf("%vx%vx%v", p.ImageWidth, p.ImageHeight, turn)
	for y := 0; y < p.ImageHeight; y++ {
		for x := 0; x < p.ImageWidth; x++ {
			c.ioOutput <- world[y][x]
		}
	}
}

func play(broker *rpc.Client, request stubs.Request, response *stubs.Response, finished chan<- bool) {
	e := broker.Call("Broker.Play", request, &response)
	handleError(e)
	finished <- true
}

func getAliveCells(paused *bool, broker *rpc.Client, c distributorChannels, finished <-chan bool) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		response := stubs.Response{AliveCells: 0}
		request := stubs.Request{}
		select {
		case <-ticker.C:
			if !*paused {
				e := broker.Call("Broker.GetAliveCells", request, &response)
				handleError(e)
				c.events <- AliveCellsCount{CellsCount: response.AliveCells, CompletedTurns: response.MessageTurns}
			}
		case <-finished:
			return
		}
	}
}

func sdlView(world [][]byte, broker *rpc.Client, c distributorChannels, p Params, finished <-chan bool) {
	previousWorld := world
	currentTurn := 0
	response := stubs.Response{MessageWorld: previousWorld}
	for {
		select {
		case <-finished:
			fmt.Println("Finished channel closed")
			return
		default:
			if currentTurn == p.Turns {
				return
			} else {
				e := broker.Call("Broker.WaitOnWorld", stubs.Request{}, &response)
				handleError(e)
				//fmt.Println(len(previousWorld))
				//fmt.Println(len(response.MessageWorld))
				for y := 0; y < p.ImageHeight; y++ {
					for x := 0; x < p.ImageWidth; x++ {
						if response.MessageWorld[y][x] != previousWorld[y][x] {
							c.events <- CellFlipped{CompletedTurns: currentTurn + 1, Cell: util.Cell{X: x, Y: y}}
						}
					}
				}
				currentTurn++
				fmt.Println(currentTurn)
				previousWorld = response.MessageWorld
				c.events <- TurnComplete{CompletedTurns: currentTurn}
			}
		}
	}
}
