package main

import (
	"flag"
	"fmt"
	"net"
	"net/rpc"
	"time"
	"uk.ac.bris.cs/gameoflife/stubs"
)

type Worker struct {
	threads int
	in      []chan [][]byte
	out     []chan [][]byte
}

func (w *Worker) Initialise(request stubs.Request, response *stubs.Response) error {
	// Logic to initialise worker's threads

	// Check if threads already exist
	if w.threads != 0 {
		fmt.Println("initialising")
		// Check if number of current threads is different to number needed
		if request.MessageThreads != w.threads {
			fmt.Println("resetting")
			closeChan(w.in)
			closeChan(w.out)
			// If number of current threads is same as number needed then don't change anything
		} else {
			fmt.Println("keeping")
			return nil
		}
	}
	w.threads = request.MessageThreads
	// Make chan to send world to each thread
	var in []chan [][]byte
	for t := 0; t < w.threads; t++ {
		in = append(in, make(chan [][]byte))
	}
	w.in = in
	// Make chan to recieve world segments from each thread
	var out []chan [][]byte
	for t := 0; t < w.threads; t++ {
		out = append(out, make(chan [][]byte))
	}
	w.out = out
	// Initialise threads
	for t := 0; t < w.threads; t++ {
		go thread(w.in[t], w.out[t])
	}
	return nil
}

func (w *Worker) Kill(re stubs.Request, response *stubs.Response) error {
	// Shut down worker and all its threads
	fmt.Println("Shutting down")

	// Create asynchronous function to close listener after rpc method has returned
	go func() {
		time.Sleep(5 * time.Second)

		// Close channels
		closeChan(w.in)
		closeChan(w.out)

		ln.Close()
	}()
	return nil
}

func (w *Worker) Evolve(request stubs.Request, response *stubs.Response) error {
	// Send segmented world to each thread
	sendWorld(request.MessageSegmentedWorld, w.in)

	// Recieve new world
	newWorld := recieveWorld(w.out)

	// Send it as response
	response.MessageWorld = newWorld
	return nil
}

func thread(in <-chan [][]byte, out chan<- [][]byte) {
	// Thread to process segments

	for {
		// Get segment coming in
		world, more := <-in

		// Check if "in" channel is open
		if more {
			height := len(world)
			width := len(world[0])

			newWorld := make([][]byte, height-2)
			for i := 0; i < height-2; i++ {
				newWorld[i] = make([]byte, width)
			}

			// GOL logic
			for y := 1; y < height-1; y++ {
				for x := 0; x < width; x++ {
					sum := int(world[(y+height-1)%height][(x+width-1)%width]) + int(world[(y+height-1)%height][(x+width)%width]) + int(world[(y+height-1)%height][(x+width+1)%width]) +
						int(world[(y+height)%height][(x+width-1)%width]) + int(world[(y+height)%height][(x+width+1)%width]) +
						int(world[(y+height+1)%height][(x+width-1)%width]) + int(world[(y+height+1)%height][(x+width)%width]) + int(world[(y+height+1)%height][(x+width+1)%width])
					sum /= 255

					switch sum {
					case 3:
						newWorld[y-1][x] = 255
					case 2:
						newWorld[y-1][x] = world[y][x]
					default:
						newWorld[y-1][x] = 0
					}
				}
			}
			// send segment out
			out <- newWorld
		} else {
			// Close goroutine if channels is closed
			break
		}
	}

}

func sendWorld(worldSegments [][][]byte, chanList []chan [][]byte) {
	// Send each segment to respective thread
	for i, c := range chanList {
		c <- worldSegments[i]
	}
}
func recieveWorld(chanList []chan [][]byte) [][]byte {
	// Recieve and join segments from threads
	var newWorld [][]byte
	for _, c := range chanList {
		newWorld = append(newWorld, <-c...)
	}
	return newWorld
}
func closeChan(chanList []chan [][]byte) {
	// Close specified channel. If "in" is closed, all threads will close
	for _, c := range chanList {
		close(c)
	}
}

var ln net.Listener

func main() {
	// Get port to listen on
	thisPort := flag.String("port", "8040", "Port to listen on")
	flag.Parse()

	// Register RPC methods
	rpc.Register(&Worker{})

	// Create listener for the broker to connect to this
	ln, _ = net.Listen("tcp", ":"+*thisPort)
	defer ln.Close()
	rpc.Accept(ln)
}

func handleError(e error) {
	if e != nil {
		panic(e)
	}
}
