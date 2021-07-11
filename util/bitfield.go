package util

import (
	"math"
	"strings"
)

type Bitfield struct {
	Bytes     []byte
	NumOfBits int
}

func NewBitfield(numOfBits int) Bitfield {
	return Bitfield{
		NumOfBits: numOfBits,
		Bytes:     make([]byte, int(math.Ceil(float64(numOfBits)/8.0))),
	}
}

// XXX This is tested in Test_bitfield_msg_assembly
func NewBitfieldFromBytes(buff []byte) Bitfield {
	return Bitfield{
		NumOfBits: len(buff) * 8,
		Bytes:     buff,
	}
}

// XXX This is tested in Test_bitfield_msg_assembly
func (self Bitfield) Set(i int) {
	// Find which byte in self.data
	byteIdx := i / 8
	bitIdx := i % 8
	self.Bytes[byteIdx] |= (1 << bitIdx)
}

// XXX This is tested in Test_bitfield_msg_assembly
func (self Bitfield) Has(i int) bool {
	// Find which byte in self.data
	byteIdx := i / 8
	bitIdx := i % 8
	return (self.Bytes[byteIdx] & (1 << bitIdx)) != 0
}

func (self Bitfield) DumpAsBitstring() string {
	var sb strings.Builder
	for i := self.NumOfBits - 1; i >= 0; i-- {
		if self.Has(i) {
			sb.WriteString("1")
		} else {
			sb.WriteString("0")
		}
	}
	return sb.String()
}

// XXX This is tested in Test_bitfield_msg_assembly
func (self Bitfield) AsIntSlice(max int) []int {
	indices := []int{}
	// XXX Include the maximum
	for i := 0; i <= max; i++ {
		if self.Has(i) {
			indices = append(indices, i)
		}
	}
	return indices
}
