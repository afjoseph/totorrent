//go:generate mockery --name=Piece
//go:generate mockery --name=SafePieces
package piece

import (
	"sync"

	"github.com/afjoseph/totorrent/util"
)

type PieceStatus int

const (
	PIECESTATUS_IDLE        PieceStatus = 0
	PIECESTATUS_DOWNLOADING PieceStatus = iota
	PIECESTATUS_DONE        PieceStatus = iota
)

type SafePieces interface {
	TryToReserve(int, string) bool
	Get(int) Piece
	First() Piece
	Last() Piece
	MaxPieceIdx() int
	Put(Piece)
	Data() map[int]Piece
	Len() int
	TotalBlocksInAllPiecesCount() int
	MarkAsDone(int)
	AsIntSlice() []int
	TotalUndonePiecesLeft() int
	AllBlocks() []*Block
	DownloadedPieceIndices() []int
	SetAsLastPiece(int)
}

type SafePiecesImpl struct {
	mutex sync.Mutex
	data  map[int]Piece
}

func NewSafePieces() SafePieces {
	return &SafePiecesImpl{data: make(map[int]Piece)}
}

func NewRandomPieces(pieceCount int, pieceLength int64, blockLength int) SafePieces {
	pieces := NewSafePieces()
	for pieceIdx := 0; pieceIdx < pieceCount; pieceIdx++ {
		p := CalculateNewPiece(pieceIdx, pieceLength, blockLength, true)
		for _, b := range p.Blocks() {
			blockBuff, err := util.RandByteSlice(blockLength)
			if err != nil {
				// XXX We can panic here since this function will only run in tests
				panic(err)
			}
			b.Buff = blockBuff
		}
		pieces.Put(p)
	}
	return pieces
}

func (self *SafePiecesImpl) MaxPieceIdx() int {
	max := 0
	for k := range self.data {
		if k > max {
			max = k
		}
	}
	return max
}

func (self *SafePiecesImpl) DownloadedPieceIndices() []int {
	pieceIndices := []int{}
	for k, v := range self.data {
		if v.Downloaded() {
			pieceIndices = append(pieceIndices, k)
		}
	}
	return pieceIndices
}

func (self *SafePiecesImpl) First() Piece {
	min := 0xffffffff
	for k := range self.data {
		if k < min {
			min = k
		}
	}
	return self.data[min]
}

func (self *SafePiecesImpl) Last() Piece {
	return self.data[self.MaxPieceIdx()]
}

func (self *SafePiecesImpl) AllBlocks() []*Block {
	blocks := []*Block{}
	// XXX This ensures that we access the pieces sequentially
	// (i.e., from 0 -> self.Len())
	for i := 0; i < self.Len(); i++ {
		blocks = append(blocks, self.data[i].Blocks()...)
	}
	return blocks
}

func (self *SafePiecesImpl) SetAsLastPiece(idx int) {
	self.data[idx].SetAsLastPiece()
}

func (self *SafePiecesImpl) AsIntSlice() []int {
	pieceIndices := []int{}
	for k := range self.data {
		pieceIndices = append(pieceIndices, k)
	}
	return pieceIndices
}

func (self *SafePiecesImpl) TotalBlocksInAllPiecesCount() int {
	sum := 0
	for _, p := range self.data {
		sum += p.BlockCount()
	}
	return sum
}

func (self *SafePiecesImpl) Len() int {
	return len(self.data)
}

func (self *SafePiecesImpl) MarkAsDone(pieceIdx int) {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	self.data[pieceIdx].SetStatus(PIECESTATUS_DONE)
}

func (self *SafePiecesImpl) TryToReserve(pieceIdx int, peerId string) bool {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	if self.data[pieceIdx].Status() != PIECESTATUS_IDLE {
		return false
	}
	self.data[pieceIdx].SetStatus(PIECESTATUS_DOWNLOADING)
	self.data[pieceIdx].SetDownloader(peerId)
	return true
}

func (self *SafePiecesImpl) Data() map[int]Piece {
	return self.data
}

// TODO Return error or nil if piece was not found
func (self *SafePiecesImpl) Get(pieceIdx int) Piece {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	return self.data[pieceIdx]
}

func (self *SafePiecesImpl) Put(p Piece) {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	self.data[p.Idx()] = p
}

func (self *SafePiecesImpl) TotalUndonePiecesLeft() int {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	it := 0
	for _, p := range self.data {
		if p.Status() != PIECESTATUS_DONE {
			it++
		}
	}
	return it
}

type Piece interface {
	SetStatus(PieceStatus)
	SetDownloader(string)
	Downloader() string
	Status() PieceStatus
	Blocks() []*Block
	BlockCount() int
	Downloaded() bool
	Len() int64
	Idx() int
	GetBlockFromOffset(uint32, uint32) *Block
	Put(*Block)
	LastPiece() bool
	SetAsLastPiece()
}

type PieceImpl struct {
	idx         int
	length      int64
	blocks      []*Block
	status      PieceStatus
	downloader  string
	isLastPiece bool
}

func NewPiece(idx int, length int64, status PieceStatus) Piece {
	return &PieceImpl{idx: idx, length: length, status: status}
}

func NewPieceWithBlocks(idx int, length int64,
	blocks []*Block) *PieceImpl {
	return &PieceImpl{idx: idx, length: length,
		blocks: blocks}
}

func CalculateNewPiece(pieceIdx int, pieceLength int64,
	blockLength int, isSeeding bool) Piece {
	p := &PieceImpl{idx: pieceIdx, length: pieceLength}
	if isSeeding {
		p.SetStatus(PIECESTATUS_DONE)
	} else {
		p.SetStatus(PIECESTATUS_IDLE)
	}

	totalBlocks := int(pieceLength) / blockLength
	remainingLength := int(pieceLength) % blockLength
	// Make all regular blocks
	p.blocks = []*Block{}
	for blockIdx := 0; blockIdx < totalBlocks; blockIdx++ {
		p.blocks = append(p.blocks, NewBlock(
			blockIdx, pieceIdx,
			uint32(blockIdx*blockLength),
			uint32(blockLength),
			nil))
	}
	// Make the last block
	if remainingLength != 0 {
		p.blocks = append(p.blocks, NewBlock(
			totalBlocks-1, pieceIdx,
			uint32(totalBlocks-1*blockLength),
			uint32(remainingLength),
			nil))
	}
	// Mark the last block
	p.blocks[len(p.blocks)-1].IsLastBlock = true

	return p
}

func (self *PieceImpl) Put(b *Block) {
	self.blocks = append(self.blocks, b)
}

func (self *PieceImpl) Downloaded() bool {
	return self.status == PIECESTATUS_DONE
}

func (self *PieceImpl) GetBlockFromOffset(blockBegin uint32,
	blockLength uint32) *Block {
	idx := blockBegin / blockLength
	if idx >= uint32(len(self.blocks)) {
		return nil
	}
	return self.blocks[idx]
}

func (self *PieceImpl) SetStatus(status PieceStatus) {
	self.status = status
}

func (self *PieceImpl) SetDownloader(peerId string) {
	self.downloader = peerId
}

func (self *PieceImpl) Downloader() string {
	return self.downloader
}

func (self *PieceImpl) Status() PieceStatus {
	return self.status
}

func (self *PieceImpl) Blocks() []*Block {
	return self.blocks
}

func (self *PieceImpl) BlockCount() int {
	return len(self.blocks)
}

func (self *PieceImpl) SetBlocks(blocks []*Block) {
	self.blocks = blocks
}

func (self *PieceImpl) Len() int64 {
	return self.length
}

func (self *PieceImpl) SetLength(i int64) {
	self.length = i
}

func (self *PieceImpl) Idx() int {
	return self.idx
}

func (self *PieceImpl) SetIdx(i int) {
	self.idx = i
}

func (self *PieceImpl) LastPiece() bool {
	return self.isLastPiece
}

func (self *PieceImpl) SetAsLastPiece() {
	self.isLastPiece = true
}
