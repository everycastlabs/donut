//go:build !js
// +build !js

package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/flavioribeiro/donut/eia608"

	astisrt "github.com/asticode/go-astisrt/pkg"
	"github.com/asticode/go-astits"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
)

var (
	//go:embed index.html
	indexHTML                   string
	api                         *webrtc.API //nolint
	peerConnectionConfiguration = webrtc.Configuration{}
	enableICEMux                = false
)

func srtToWebRTC(srtConnection *astisrt.Connection, videoTrack *webrtc.TrackLocalStaticSample, metadataTrack *webrtc.DataChannel) {
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	defer srtConnection.Close()
	log.Printf("Staring  forwarder\n")

	go func() {
		defer srtConnection.Close()
		inboundMpegTsPacket := make([]byte, 1316) // SRT Read Size

		for {
			n, err := srtConnection.Read(inboundMpegTsPacket)
			if err != nil {
				break
			}
			log.Printf("Read frame , length = %d\n", n)

			if _, err := w.Write(inboundMpegTsPacket[:n]); err != nil {
				break
			}
		}
	}()

	dmx := astits.NewDemuxer(context.Background(), r)
	eia608Reader := eia608.NewEIA608Reader()
	h264PID := uint16(0)
	for {
		d, err := dmx.NextData()
		if err != nil {
			log.Printf("failed to find next frame because %s \n", err)
			break
		}

		if d.PMT != nil && metadataTrack != nil {
			for _, es := range d.PMT.ElementaryStreams {
				msg, _ := json.Marshal(struct {
					Type    string
					Message string
				}{
					Type:    "metadata",
					Message: es.StreamType.String(),
				})
				metadataTrack.SendText(string(msg))
				if es.StreamType == astits.StreamTypeH264Video {
					h264PID = es.ElementaryPID
				}
			}

			for _, d := range d.PMT.ProgramDescriptors {
				if d.MaximumBitrate != nil {
					bitrateInMbitsPerSecond := float32(d.MaximumBitrate.Bitrate) / float32(125000)
					msg, _ := json.Marshal(struct {
						Type    string
						Message string
					}{
						Type:    "metadata",
						Message: fmt.Sprintf("Bitrate %.2fMbps", bitrateInMbitsPerSecond),
					})
					metadataTrack.SendText(string(msg))
				}
			}
		}
		//log.Printf("Got frame , pid = %d\n", d.PID)
		if d.PID == h264PID && d.PES != nil {
			log.Printf("Sending frame , length = %d\n", len(d.PES.Data))

			err = videoTrack.WriteSample(media.Sample{Data: d.PES.Data, Duration: time.Second / 30})
			if err != nil {
				log.Printf("cant write frame of length %d \n", len(d.PES.Data))
				break
			}
			captions, err := eia608Reader.Parse(d.PES)
			if err != nil {
				log.Printf("cant parse frame of length %d \n", len(d.PES.Data))
				break
			}
			if captions != "" && metadataTrack != nil {
				captionsMsg, err := eia608.BuildCaptionsMessage(d.PES.Header.OptionalHeader.PTS, captions)
				if err != nil {
					log.Printf("cant caption frame of length %d \n", len(d.PES.Data))
					break
				}
				metadataTrack.SendText(captionsMsg)
			}
		}
	}

}
func doSignaling(w http.ResponseWriter, r *http.Request) {
	setCors(w, r)
	if r.Method != http.MethodPost {
		return
	}

	peerConnection, err := api.NewPeerConnection(peerConnectionConfiguration)
	if err != nil {
		errorToHTTP(w, err)
		return
	}

	offer := struct {
		SRTHost     string
		SRTPort     string
		SRTStreamID string
		Offer       webrtc.SessionDescription
	}{"", "", "", webrtc.SessionDescription{}}

	if err = json.NewDecoder(r.Body).Decode(&offer); err != nil {
		errorToHTTP(w, err)
		return
	}

	// Create a video track
	videoTrack, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264}, "video", offer.SRTStreamID)
	if err != nil {
		errorToHTTP(w, err)
		return
	}
	if _, err := peerConnection.AddTrack(videoTrack); err != nil {
		errorToHTTP(w, err)
		return
	}

	// Create data channel for metadata transmission
	metadataSender, err := peerConnection.CreateDataChannel("metadata", nil)
	if err != nil {
		errorToHTTP(w, err)
	}

	// Set the handler for ICE connection state
	// This will notify you when the peer has connected/disconnected
	peerConnection.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		log.Printf("ICE Connection State has changed: %s\n", connectionState.String())
	})

	srtPort, err := assertSignalingCorrect(offer.SRTHost, offer.SRTPort, offer.SRTStreamID)
	if err != nil {
		errorToHTTP(w, err)
		return
	}

	if err = peerConnection.SetRemoteDescription(offer.Offer); err != nil {
		errorToHTTP(w, err)
		return
	}

	log.Println("Gathering WebRTC Candidates")
	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)
	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		errorToHTTP(w, err)
		return
	} else if err = peerConnection.SetLocalDescription(answer); err != nil {
		errorToHTTP(w, err)
		return
	}
	<-gatherComplete
	log.Println("Gathering WebRTC Candidates Complete")

	response, err := json.Marshal(*peerConnection.LocalDescription())
	if err != nil {
		return
	}

	log.Println("Connecting to SRT ", offer.SRTHost, srtPort, offer.SRTStreamID)
	srtConnection, err := astisrt.Dial(astisrt.DialOptions{
		ConnectionOptions: []astisrt.ConnectionOption{
			astisrt.WithLatency(300),
			astisrt.WithStreamid(offer.SRTStreamID),
		},

		// Callback when the connection is disconnected
		OnDisconnect: func(c *astisrt.Connection, err error) { log.Fatal("Disconnected from SRT") },

		Host: offer.SRTHost,
		Port: uint16(srtPort),
	})
	if err != nil {
		errorToHTTP(w, err)
		return
	}
	log.Println("Connected to SRT")

	go srtToWebRTC(srtConnection, videoTrack, metadataSender)

	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(response); err != nil {
		errorToHTTP(w, err)
		return
	}
}

func doWhip(whipUri string, srtUri string, token string, a *webrtc.API) {
	// start by doing the whip side - so we are _ready_ when srt sends the first frame.
	whip := NewWHIPClient(whipUri, token)
	whip.Publish(a, peerConnectionConfiguration)

}

func main() {
	whipUri := flag.String("whip-uri", "", "whip URI to send stream to")
	srtUri := flag.String("srt-uri", "", "srt URI to use")

	flag.BoolVar(&enableICEMux, "enable-ice-mux", false, "Enable ICE Mux on :8081")
	flag.Parse()

	mediaEngine := &webrtc.MediaEngine{}
	settingEngine := webrtc.SettingEngine{}
	if err := mediaEngine.RegisterDefaultCodecs(); err != nil {
		log.Fatal(err)
	}

	if enableICEMux {
		tcpListener, err := net.ListenTCP("tcp", &net.TCPAddr{
			IP:   net.IP{0, 0, 0, 0},
			Port: 8081,
		})
		if err != nil {
			log.Fatal(err)
		}

		udpListener, err := net.ListenUDP("udp", &net.UDPAddr{
			IP:   net.IP{0, 0, 0, 0},
			Port: 8081,
		})
		if err != nil {
			log.Fatal(err)
		}

		settingEngine.SetNAT1To1IPs([]string{"127.0.0.1"}, webrtc.ICECandidateTypeHost)
		settingEngine.SetICETCPMux(webrtc.NewICETCPMux(nil, tcpListener, 8))
		settingEngine.SetICEUDPMux(webrtc.NewICEUDPMux(nil, udpListener))
	}
	api = webrtc.NewAPI(webrtc.WithSettingEngine(settingEngine), webrtc.WithMediaEngine(mediaEngine))
	peerConnectionConfiguration = webrtc.Configuration{}
	if !enableICEMux {
		peerConnectionConfiguration.ICEServers = []webrtc.ICEServer{
			{
				URLs: []string{
					"stun:stun4.l.google.com:19302",
				},
			},
		}
	}
	if len(*whipUri) > 0 && len(*srtUri) > 0 {
		token := flag.String("whip-token", "", "bearer token for whip")
		log.Println("WHIP mode - sending %s to %s \n ", whipUri, srtUri)

		doWhip(*whipUri, *srtUri, *token, api)
	} else {
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
			w.Write([]byte(indexHTML))
		})
		http.HandleFunc("/doSignaling", doSignaling)

		log.Println("Open http://localhost:8080 to access this demo")
		log.Fatal(http.ListenAndServe(":8080", nil))
	}
}
