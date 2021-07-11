package peer

import (
	"net"

	"github.com/afjoseph/totorrent/fileinfo"
)

// NewClientPeer makes a new "Client Peer" (which basically means us: the
// client) from a metainfo. This project makes the assumption that each peer is
// coupled to ONE torrent file
func NewClientPeer(
	fileInfo *fileinfo.FileInfo,
	onConnectFunc OnConnectFunc,
	peerMsgFunc PeerMsgFunc,
	sendHandshakeFunc SendHandshakeFunc,
	doTrackMsgs bool,
) (*Peer, error) {
	return &Peer{
		Name:              "ClientPeer",
		IP:                net.IPv4(127, 0, 0, 1),
		Port:              0, // We'll never use this port so you can keep it nil
		ID:                MakePeerId(),
		FileInfo:          fileInfo,
		OnConnectFunc:     nil,
		PeerMsgFunc:       peerMsgFunc,
		SendHandshakeFunc: sendHandshakeFunc,
		doTrackMsgs:       doTrackMsgs,
	}, nil
}
