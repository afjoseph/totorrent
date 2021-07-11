package peer

import (
	"fmt"
	"net"
	"strconv"

	"github.com/afjoseph/commongo/print"
	"github.com/afjoseph/totorrent/fileinfo"
	"github.com/afjoseph/totorrent/util"
)

func NewTestPeer(
	idx int,
	fileInfo *fileinfo.FileInfo,
	onConnectFunc OnConnectFunc,
	peerMsgFunc PeerMsgFunc,
	sendHandshakeFunc SendHandshakeFunc,
) *Peer {
	// Find a random open port
	var portAsString string
	for {
		portAsString = fmt.Sprintf("%d%d%d%d%d",
			util.RandRange(1, 3),
			util.RandRange(1, 3),
			util.RandRange(1, 3),
			util.RandRange(1, 3),
			util.RandRange(1, 3),
		)
		if !util.IsPortTaken(portAsString) {
			break
		}
		print.Debugf(
			"TestPeer port %s is taken: picking another...\n",
			portAsString)
	}
	portAsInt, err := strconv.Atoi(portAsString)
	if err != nil {
		// We can panic since this will only run in tests
		panic(err)
	}
	// Convert id into a fixed-size array
	idAsSlice := []byte("-testme-" + portAsString)
	var idAsArray [20]byte
	copy(idAsArray[:], idAsSlice)
	return &Peer{
		Name:              fmt.Sprintf("TestPeer #%d", idx),
		IP:                net.IPv4(127, 0, 0, 1),
		Port:              portAsInt,
		ID:                idAsArray,
		OnConnectFunc:     onConnectFunc,
		PeerMsgFunc:       peerMsgFunc,
		SendHandshakeFunc: sendHandshakeFunc,
		doTrackMsgs:       true,
		FileInfo:          fileInfo,
	}
}
