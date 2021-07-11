package announce

import (
	"crypto/sha1"
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"

	"github.com/afjoseph/commongo/print"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/afjoseph/totorrent/peer"
)

var (
	ERR_ANNOUNCEURL    = errors.New("ERR_ANNOUNCEURL")
	ERR_QUERY_ANNOUNCE = errors.New("ERR_QUERY_ANNOUNCE")
)

const (
	ANNOUNCEURLKIND_DHT  = iota
	ANNOUNCEURLKIND_UDP  = iota
	ANNOUNCEURLKIND_HTTP = iota
)

type AnnounceUrl struct {
	url  string
	kind uint
}

func NewAnnounceUrl(url string) (*AnnounceUrl, error) {
	switch {
	case strings.HasPrefix(url, "dht"):
		return &AnnounceUrl{url, ANNOUNCEURLKIND_DHT}, nil
	case strings.HasPrefix(url, "udp"):
		return &AnnounceUrl{url, ANNOUNCEURLKIND_UDP}, nil
	case strings.HasPrefix(url, "http"):
		return &AnnounceUrl{url, ANNOUNCEURLKIND_HTTP}, nil
	// TODO: Support HTTPS
	// case strings.HasPrefix("https"):
	default:
		return nil, print.ErrorWrapf(ERR_ANNOUNCEURL, "url [%s] has no supported protocol", url)
	}
}

type AnnounceHandshakeParams struct {
	InfoHash   string
	PeerId     string
	Port       string
	Uploaded   string
	Downloaded string
	Left       string
	Event      string
}

type AnnounceUrlResponse struct {
	FailureReason string     `bencode:"failure reason"`
	Interval      int32      `bencode:"interval"`
	TrackerId     string     `bencode:"tracker id"`
	Complete      int32      `bencode:"complete"`
	Incomplete    int32      `bencode:"incomplete"`
	Peers         peer.Peers `bencode:"peers"`
}

// QueryAnnounceUrls takes 'announceUrls' and queries them with
// 'handshakeParams' synchronously until one responds.
// If we get one good response, it is decoded (through bencode), marshalled and
// returned
func QueryAnnounceUrls(announceUrls []string,
	handshakeParams AnnounceHandshakeParams) (*AnnounceUrlResponse, error) {
	print.DebugFunc()

	// Get Usable URL
	// XXX In the future, this should work for all types
	usableAnnounceUrls := []string{}
	for _, announceUrl := range announceUrls {
		if !strings.HasPrefix(announceUrl, "http") {
			continue
		}
		usableAnnounceUrls = append(usableAnnounceUrls, announceUrl)
	}
	if len(usableAnnounceUrls) == 0 {
		return nil,
			print.ErrorWrapf(ERR_QUERY_ANNOUNCE, "Found no usable announce URLS")
	}

	// Run query
	// XXX Logic here loops synchronously through all usable announceUrls until
	// we get one response
	client := &http.Client{}
	foundValidResp := false
	decodedRespBody := &AnnounceUrlResponse{}
	for _, announceUrl := range usableAnnounceUrls {
		req, err := http.NewRequest("GET", announceUrl, nil)
		if err != nil {
			return nil, print.ErrorWrapf(ERR_QUERY_ANNOUNCE, err.Error())
		}
		q := req.URL.Query()
		q.Add("info_hash", handshakeParams.InfoHash)
		q.Add("peer_id", handshakeParams.PeerId)
		q.Add("port", handshakeParams.Port)
		q.Add("uploaded", handshakeParams.Uploaded)
		q.Add("downloaded", handshakeParams.Downloaded)
		q.Add("left", handshakeParams.Left)
		q.Add("event", handshakeParams.Event)
		req.URL.RawQuery = q.Encode()
		reqDump, _ := httputil.DumpRequest(req, false)
		print.Debugf("Running request: %+v\n", string(reqDump))
		resp, err := client.Do(req)
		if err != nil {
			print.Warnf("Failed to query announce url [%s] with err [%s]\n",
				announceUrl, err.Error())
			continue
		}
		// Handle statuscode
		if !(resp.StatusCode >= 100 && resp.StatusCode <= 299) {
			print.Warnf("Can't handle response with status code: [%d] from url [%s]\n",
				resp.StatusCode, announceUrl)
			continue
		}

		// Extract and decode body
		// XXX Body here is always expected to be bencoded
		defer resp.Body.Close()
		encodedRespBody, _ := io.ReadAll(resp.Body)
		err = bencode.Unmarshal(encodedRespBody, decodedRespBody)
		if err != nil {
			print.Warnf(
				"Response body of announce url query to <%s> was not valid bencode: trying the announce URL...\n",
				announceUrl,
			)
			continue
		}
		foundValidResp = true
		print.Debugf("%+v\n", decodedRespBody)
		// Break if we get one good response
		break
	}

	// Handle tracker failure messages
	if len(decodedRespBody.FailureReason) != 0 {
		return decodedRespBody, print.ErrorWrapf(ERR_QUERY_ANNOUNCE,
			"Query was successful, but tracker returned a failure: [%s]",
			decodedRespBody.FailureReason)
	}
	if !foundValidResp {
		return nil, print.ErrorWrapf(
			ERR_QUERY_ANNOUNCE, "Failed to find a valid bencoded response from any announce URL")
	}

	return decodedRespBody, nil
}

func GetAnnounceHandshakeParams(myMetainfo *metainfo.MetaInfo, myPeerId [20]byte) (AnnounceHandshakeParams, error) {
	print.DebugFunc()

	var handshakeParams AnnounceHandshakeParams
	hash := sha1.Sum(myMetainfo.InfoBytes)
	handshakeParams.InfoHash = string(hash[:])
	handshakeParams.PeerId = string(myPeerId[:])
	handshakeParams.Port = "6881"
	handshakeParams.Uploaded = "0"
	handshakeParams.Downloaded = "0"

	// Calculate Left
	myInfo, err := myMetainfo.UnmarshalInfo()
	if err != nil {
		return AnnounceHandshakeParams{}, err
	}
	var totalLength uint64
	if myInfo.Length != 0 {
		// This is a single-file torrent
		totalLength = uint64(myInfo.Length)
	} else {
		// This is a multi-file torrent
		for _, file := range myInfo.Files {
			totalLength += uint64(file.Length)
		}
	}
	handshakeParams.Left = strconv.FormatUint(totalLength, 10)
	handshakeParams.Event = "started"
	// From wireshark capture:
	// Request URI Query Parameter: info_hash=%2fZ%5c%cb%7d%c3%2b%7f%7d%7b%15%0d%d6%ef%bc%e8%7d%2f%c3q
	// Request URI Query Parameter: peer_id=-qB4350-WsWWrE6(JJbK
	// Request URI Query Parameter: port=48669
	// Request URI Query Parameter: uploaded=0
	// Request URI Query Parameter: downloaded=0
	// Request URI Query Parameter: left=2176671920
	// Request URI Query Parameter: corrupt=0
	// Request URI Query Parameter: key=97DB36A1
	// Request URI Query Parameter: event=started
	// Request URI Query Parameter: numwant=200
	// Request URI Query Parameter: compact=1
	// Request URI Query Parameter: no_peer_id=1
	// Request URI Query Parameter: supportcrypto=1
	// Request URI Query Parameter: redundant=0
	return handshakeParams, nil
}

func GetPeers(myMetainfo *metainfo.MetaInfo, myPeerId [20]byte) (*AnnounceUrlResponse, error) {
	print.DebugFunc()

	// Get handshake params
	handshakeParams, err := GetAnnounceHandshakeParams(myMetainfo, myPeerId)
	if err != nil {
		return nil, err
	}

	// Query announce URLs
	announceUrls := []string{}
	announceUrls = append(announceUrls, myMetainfo.Announce)
	for _, l := range myMetainfo.AnnounceList {
		announceUrls = append(announceUrls, l[0])
	}
	for _, l := range myMetainfo.UrlList {
		announceUrls = append(announceUrls, l)
	}
	announceResponse, err := QueryAnnounceUrls(announceUrls, handshakeParams)
	if err != nil {
		return nil, err
	}
	return announceResponse, nil
}
