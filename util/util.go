package util

import (
	"errors"
	"math/rand"
	"net"
	"os"
	"time"

	"github.com/afjoseph/commongo/print"
	"github.com/anacrolix/torrent/metainfo"
)

var ERR_DECODE_FILE = errors.New("ERR_DECODE_FILE")
var ERR_RAND_BYTESLICE = errors.New("ERR_RAND_BYTESLICE")

func Min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func DecodeTorrentFile(filePath string) (*metainfo.MetaInfo, error) {
	print.DebugFunc()
	// TODO: This is a cop-out but it works for now
	mi, err := metainfo.LoadFromFile(filePath)
	if err != nil {
		return nil, print.ErrorWrapf(ERR_DECODE_FILE, err.Error())
	}
	return mi, nil
}

func RandRange(min, max int) int {
	return rand.Intn(max-min) + max
}

func RandByteSlice(max int) ([]byte, error) {
	b := make([]byte, max)
	_, err := rand.Read(b)
	if err != nil {
		return nil, print.ErrorWrapf(ERR_RAND_BYTESLICE, err.Error())
	}
	return b, nil
}

func GetIpAndPortFromNetAddr(addr net.Addr) (net.IP, int) {
	var ip net.IP
	var port int
	switch typedAddr := addr.(type) {
	case *net.UDPAddr:
		ip = typedAddr.IP
		port = typedAddr.Port
	case *net.TCPAddr:
		ip = typedAddr.IP
		port = typedAddr.Port
	}
	return ip, port
}

func IsFile(path string) bool {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !fileInfo.IsDir()
}

func IsDirectory(path string) bool {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return false
	}
	return fileInfo.IsDir()
}

func MkdirTempOrPanic(dir, pattern string) string {
	tmpdir, err := os.MkdirTemp(dir, pattern)
	if err != nil {
		panic(err)
	}
	return tmpdir
}

func MkdirAllOrPanic(dir string) {
	err := os.MkdirAll(dir, os.ModePerm)
	if err != nil {
		panic(err)
	}
}

func IntSliceHas(haystack []int, needle int) bool {
	for _, k := range haystack {
		if needle == k {
			return true
		}
	}
	return false
}

func IntSliceAppendIfUnique(slice []int, elem int) []int {
	didFind := false
	for _, v := range slice {
		if v == elem {
			didFind = true
			break
		}
	}
	if !didFind {
		slice = append(slice, elem)
	}
	return slice
}

func IntSliceUniqueMergeSlices(sliceA, sliceB []int) []int {
	mergedSlice := []int{}
	// 2,3,4,5,6,10,20
	// 10,20,30,40

	for _, v := range sliceA {
		mergedSlice = IntSliceAppendIfUnique(mergedSlice, v)
	}
	for _, v := range sliceB {
		mergedSlice = IntSliceAppendIfUnique(mergedSlice, v)
	}
	return mergedSlice
}

func IsPortTaken(port string) bool {
	conn, _ := net.DialTimeout("tcp",
		net.JoinHostPort("", port), 1*time.Second)
	if conn != nil {
		conn.Close()
		return true
	}
	return false
}
