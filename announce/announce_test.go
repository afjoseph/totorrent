package announce

import (
	"log"
	"os"
	"path/filepath"
	"testing"

	"github.com/afjoseph/commongo/print"
	"github.com/afjoseph/totorrent/peer"
	"github.com/afjoseph/totorrent/projectpath"
	"github.com/afjoseph/totorrent/util"
	"github.com/stretchr/testify/require"
)

func Test_GetPeers_Online(t *testing.T) {
	if len(os.Getenv("ONLINE")) == 0 {
		t.Skipf("Skipping ONLINE test")
	}
	print.SetLevel(print.LOG_DEBUG)

	myMetainfo, err := util.DecodeTorrentFile(filepath.Join(projectpath.Root,
		"test_assets/multi_file.torrent"))
	require.NoError(t, err)
	require.NotNil(t, myMetainfo)
	myInfo, err := myMetainfo.UnmarshalInfo()
	require.NoError(t, err)
	log.Printf("myInfo.Pieces: %d\n", len(myInfo.Pieces))

	myPeerId := peer.MakePeerId()
	_, err = GetPeers(myMetainfo, myPeerId)
	require.NoError(t, err)
}
