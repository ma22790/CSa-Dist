package main

import (
	"bufio"
	"fmt"
	"math"
	"net"
	"net/rpc"
	"os"
	"strings"
	"sync"
	"time"
	"uk.ac.bris.cs/gameoflife/stubs"
)

type Broker struct {
	// Current parameters stored in the broker
	World      [][]byte
	Turns      int
	MaxTurns   int
	Height     int
	Threads    int
	Workers    int
	CanProcess sync.Mutex
	Paused     bool
	killed     chan struct{}

	ln net.Listener
}

func (b *Broker) Play(request stubs.Request, response *stubs.Response) error {
	// Unpause broker
	if b.Paused {
		// Unlock ability to process
		b.CanProcess.Unlock()
		b.Paused = false
	}

	//b.sender = make(chan bool)
	//b.reciever = make(chan bool)

	// Set initial broker variables

	b.World = request.MessageWorld
	b.MaxTurns = request.MessageTurns
	b.Height = request.MessageHeight
	//b.Width = request.MessageWidth
	b.Threads = request.MessageThreads
	b.Turns = 0
	b.Workers = int(math.Min(float64(len(workers)), float64(b.Threads)))
	b.killed = make(chan struct{})

	// Get chunks that indicate how board is split between threads, split between workers
	chunks := evenSplit(b.Height, b.Threads, b.Workers)
	//fmt.Println(chunks)

	// Create chanList to recieve segmented board after each evolution
	var out []chan [][]byte
	for t := 0; t < b.Workers; t++ {
		out = append(out, make(chan [][]byte, 1))
	}

	for i := 0; i < b.Workers; i++ {
		e := workers[i].Call("Worker.Initialise", stubs.Request{MessageThreads: len(chunks[i])}, &(stubs.Response{}))
		handleError(e)
	}

	// Begin processing turns
	for b.Turns = 0; b.Turns < b.MaxTurns; b.Turns++ {
		select {
		// Check if killed channel is closed (channels send a signal when closed)
		case <-b.killed:
			//fmt.Println("Ending GOL Loop")
			return nil

		// if no signal then continue with GOL logic
		default:
			// Segment board by number of threads, then split between number of workers, and add padding/overlap
			formattedWorld := formatWorld(b.World, chunks)

			// Call workers asyncronously to evolve the board
			var wg sync.WaitGroup
			for i := 0; i < b.Workers; i++ {
				// Increment the WaitGroup counter
				wg.Add(1)

				go func(index int) {
					defer wg.Done() // Decrement the counter when the goroutine completes
					tempRes := stubs.Response{}

					// Perform the RPC call
					e := workers[index].Call("Worker.Evolve", stubs.Request{MessageSegmentedWorld: formattedWorld[index]}, &tempRes)
					handleError(e)

					// Send processed segment out through channel
					out[index] <- tempRes.MessageWorld
				}(i)
			}

			// Wait for all goroutines to finish
			wg.Wait()

			// Merge segments
			var newWorld [][]byte
			for _, c := range out {
				newWorld = append(newWorld, <-c...)
			}

			// Lock critical section
			b.CanProcess.Lock()

			// Update current broker variables + response
			b.World = newWorld
			response.MessageWorld = newWorld
			response.MessageTurns = b.Turns + 1

			// Unlock critical section
			b.CanProcess.Unlock()
		}

	}
	return nil
}

func (b *Broker) WaitOnWorld(re stubs.Request, response *stubs.Response) error {
	//response.MessageWorld = <-b.se
	//b.re <- true
	return nil
}

func (b *Broker) GetCurrentWorld(re stubs.Request, response *stubs.Response) error {
	response.MessageWorld = b.World
	return nil
}

func (b *Broker) GetAliveCells(re, response *stubs.Response) error {
	// Check if unpaused
	if !b.Paused {
		aliveCells := 0
		// Grab current broker world and turns passed
		currentWorld := b.World
		currentTurns := b.Turns
		//fmt.Println("Turns passed: ", currentTurns)
		for _, line := range currentWorld {
			for _, cell := range line {
				if cell == 255 {
					aliveCells++
				}
			}
		}
		response.AliveCells = aliveCells
		response.MessageTurns = currentTurns
	}
	return nil
}

func (b *Broker) TogglePaused(re stubs.Request, response *stubs.Response) error {
	// Check if paused
	if !b.Paused {
		// If unpaused then pause and lock processing
		b.CanProcess.Lock()
	} else {
		// Else unlock processing
		b.CanProcess.Unlock()
	}

	// Toggle paused
	b.Paused = !b.Paused
	return nil
}

func (b *Broker) Kill(re stubs.Request, response *stubs.Response) error {
	fmt.Println("Shutting down broker...")
	// Stop GOL logic loop
	close(b.killed)

	// Shutdown all workers gracefully
	for _, worker := range workers {
		e := worker.Call("Worker.Kill", stubs.Request{}, &response)
		handleError(e)
	}

	// Close the listener to stop accepting connections
	go func() {
		time.Sleep(2 * time.Second)
		e := ln.Close()
		handleError(e)
	}()
	return nil
}

func evenSplit(imageHeight int, threads int, workers int) [][]int {
	var list []int
	for x := 1; x <= threads; x++ {
		list = append(list, imageHeight/(threads))
		if x <= imageHeight%threads {
			list[x-1] = list[x-1] + 1
		}
	}

	chunks := make([][]int, workers)
	for i, item := range list {
		if item > 0 {
			chunks[i%workers] = append(chunks[i%workers], item)
		}
	}
	return chunks
}

func formatWorld(world [][]byte, chunks [][]int) [][][][]byte {
	// Segment world by number of threads, then split between number of workers, and add padding/overlap

	var formattedWorld [][][][]byte
	rowNo := 0
	for workerNo := 0; workerNo < len(chunks); workerNo++ {
		//fmt.Println("worker: ", workerNo+1)
		var worker [][][]byte
		for threadNo := 0; threadNo < len(chunks[workerNo]); threadNo++ {
			var thread [][]byte

			// If it is the first thread then overlap top is the bottom of the world
			if workerNo == 0 && threadNo == 0 {
				thread = append(thread, world[len(world)-1])
			} else {
				thread = append(thread, world[rowNo])
				rowNo++
			}

			for i := 0; i < chunks[workerNo][threadNo]; i++ {
				thread = append(thread, world[rowNo])
				rowNo++
			}

			// If it is the first thread then overlap bottom is the top of the world
			if workerNo == len(chunks)-1 && threadNo == len(chunks[workerNo])-1 {
				thread = append(thread, world[0])
			} else {
				thread = append(thread, world[rowNo])
				rowNo--
			}
			worker = append(worker, thread)
		}
		formattedWorld = append(formattedWorld, worker)
	}
	return formattedWorld
}

func handleError(e error) {
	if e != nil {
		panic(e)
	}
}

func connectWorkers() {
	// Connect AWS workers
	// Get IP of each AWS worker
	stdin := bufio.NewReader(os.Stdin)
	workerID := 0
	for {
		fmt.Printf("Enter IP:Port of AWS worker %d: ", workerID)
		addr, err := stdin.ReadString('\n')
		if err != nil {
			handleError(err)
		}

		// Trim whitespace and newline characters from the address
		addr = strings.TrimSpace(addr)
		//addr = "localhost:" + addr

		if addr == "end" {
			fmt.Println("Finished connecting")
			break
		} else {
			worker, err := rpc.Dial("tcp", addr)
			handleError(err)
			workers[workerID] = worker
			workerID++
		}
	}
}

var workers = make(map[int]*rpc.Client)
var ln net.Listener

func main() {
	fmt.Println(ln)
	// Register broker
	e := rpc.Register(&Broker{})
	handleError(e)

	// Create listener for the controller
	ln, _ = net.Listen("tcp", ":8030")

	defer ln.Close()

	// Connect to AWS workers
	connectWorkers()
	rpc.Accept(ln)

}
