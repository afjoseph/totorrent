package main

import (
	"errors"
	"flag"
	"os"

	"github.com/afjoseph/commongo/print"
	"github.com/afjoseph/totorrent/announce"
	"github.com/afjoseph/totorrent/fileinfo"
	"github.com/afjoseph/totorrent/peer"
	"github.com/afjoseph/totorrent/piece"
	"github.com/afjoseph/totorrent/util"
)

var (
	fileFlag             = flag.String("torrent_file", "", "")
	modeFlag             = flag.String("mode", "", "")
	downloadDirFlag      = flag.String("download_dir", "", "")
	ERR_DOWNLOAD_TORRENT = errors.New("ERR_DOWNLOAD_TORRENT")
	ERR_INSPECT_TORRENT  = errors.New("ERR_INSPECT_TORRENT")
)

func downloadTorrent() error {
	if len(*downloadDirFlag) == 0 {
		return print.ErrorWrapf(ERR_DOWNLOAD_TORRENT, "downloadDirFlag is empty")
	}
	if len(*fileFlag) == 0 {
		return print.ErrorWrapf(ERR_DOWNLOAD_TORRENT, "fileFlag is empty")
	}

	// Decode and unmarshal info from metainfo
	myMetainfo, err := util.DecodeTorrentFile(*fileFlag)
	if err != nil {
		return err
	}
	myInfo, err := myMetainfo.UnmarshalInfo()
	if err != nil {
		return err
	}
	// Make a new FileInfo from myInfo
	fileInfo, err := fileinfo.NewFileInfo(
		len(myInfo.Pieces)/20,
		myInfo.PieceLength,
		piece.BLOCK_LENGTH,
		*downloadDirFlag,
		util.MkdirTempOrPanic("", "totorrenttmp_"),
		&myInfo,
		fileinfo.DefaultFinalizeFunc,
		false,
	)
	if err != nil {
		return err
	}
	// Supply the values to NewClientPeer
	clientPeer, err := peer.NewClientPeer(
		fileInfo,
		nil,
		peer.DefaultPeerMsgFunc,
		peer.DefaultSendHandshakeFunc,
		false,
	)
	if err != nil {
		return err
	}
	// Fetch peers from announce url
	peerId := peer.MakePeerId()
	announceResponse, err := announce.GetPeers(myMetainfo, peerId)
	if err != nil {
		return err
	}
	// Connect with all peers
	// XXX This is a blocking call
	err = clientPeer.ConnectWithAllPeers(
		myMetainfo,
		announceResponse.Peers, 2,
		func(targetPeer *peer.Peer, err error) {
			print.Warnf("Peer [%s] failed with err: %v\n", targetPeer, err)
		},
	)
	if err != nil {
		return err
	}

	return nil
}

func inspectTorrent() error {
	if len(*fileFlag) == 0 {
		return print.ErrorWrapf(ERR_INSPECT_TORRENT, "fileFlag is empty")
	}

	myMetainfo, err := util.DecodeTorrentFile(*fileFlag)
	if err != nil {
		return err
	}
	myInfo, err := myMetainfo.UnmarshalInfo()
	if err != nil {
		return err
	}
	print.Debugf("Torrent: %+v\n", *fileFlag)
	print.Debugf("MetaInfo: %+v\n", myMetainfo)
	print.Debugf("Info: %+v\n", myInfo)

	return nil
}

func run() error {
	print.SetLevel(print.LOG_DEBUG)
	flag.Parse()

	var err error
	switch *modeFlag {
	case "inspect":
		err = inspectTorrent()
	case "download":
		err = downloadTorrent()
	default:
		print.Warnln("Bad flag")
	}
	if err != nil {
		return err
	}

	return nil
}

func main() {
	exitCode := 0
	err := run()
	if err != nil {
		print.Warnln(err.Error())
		exitCode = 1
	}
	<-print.Flush()
	os.Exit(exitCode)
}
