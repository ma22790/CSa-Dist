package stubs

type Response struct {
	MessageString         int
	MessageWorld          [][]byte
	MessageSegmentedWorld [][][]byte
	MessageHeight         int
	MessageTurns          int
	MessageThreads        int
	AliveCells            int
}

type Request struct {
	MessageString         int
	MessageWorld          [][]byte
	MessageSegmentedWorld [][][]byte
	MessageHeight         int
	MessageTurns          int
	MessageThreads        int
	AliveCells            int
}
