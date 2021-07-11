package piece

import (
	"fmt"
	"os"
	"strings"

	"github.com/afjoseph/totorrent/util"
)

const BLOCK_LENGTH = 0x4000

type Block struct {
	// XXX Idx is not really something in the specs, but a construct I made to
	// simplify categorizing blocks
	Idx              int
	PieceIdx         int
	Begin            uint32
	Buff             []byte
	Length           uint32
	DownloaderPeerId []byte
	IsLastBlock      bool
}

func NewBlock(blockIdx int, pieceIdx int, begin uint32,
	length uint32, buff []byte) *Block {
	return &Block{
		Idx:      blockIdx,
		PieceIdx: pieceIdx,
		Begin:    begin,
		Buff:     buff,
		Length:   length,
	}
}

func NewBlockFromFile(pieceIdx int, begin uint32, payloadFilePath string) *Block {
	payload, err := os.ReadFile(payloadFilePath)
	if err != nil {
		// It is safe to panic here since we're using this function exclusively
		// in tests and it is more convenient to not check for errors and keep
		// the function signature simple
		panic(err)
	}
	return NewBlock(0, pieceIdx, begin, uint32(len(payload)), payload)
}

func NewRandomBlock(pieceIdx, blockIdx, maxBuffSize int) *Block {
	randBuff, err := util.RandByteSlice(util.RandRange(0, maxBuffSize))
	if err != nil {
		// XXX We can panic here since this function will only run in tests
		panic(err)
	}
	return NewBlock(
		pieceIdx,
		blockIdx,
		uint32(util.RandRange(0, 0xffff)),
		uint32(len(randBuff)),
		randBuff,
	)
}

func (self *Block) String() string {
	var sb strings.Builder
	if len(self.Buff) > 4 {
		sb.WriteString(fmt.Sprintf("Block{PieceIdx:%d | blockIdx:%d, len:%d, buff[0:4]:%x}",
			self.PieceIdx, self.Idx,
			len(self.Buff), self.Buff[:4]))
	} else {
		sb.WriteString(fmt.Sprintf("Block{PieceIdx:%d | blockIdx:%d, len:%d}",
			self.PieceIdx, self.Idx, len(self.Buff)))
	}
	return sb.String()
}
