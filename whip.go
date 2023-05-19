package main

import (
	"bytes"
	"crypto/tls"
	"github.com/pion/webrtc/v3"
	"io"
	"log"
	"net/http"
	"net/url"
)

type WHIPClient struct {
	endpoint    string
	token       string
	resourceUrl string
}

func NewWHIPClient(endpoint string, token string) *WHIPClient {
	client := new(WHIPClient)
	client.endpoint = endpoint
	client.token = token
	return client
}

func (whip *WHIPClient) Publish(napi *webrtc.API, config webrtc.Configuration) *webrtc.TrackLocalStaticSample {

	log.Printf("new PeerConnection \n")

	pc, err := napi.NewPeerConnection(config)
	if err != nil {
		log.Fatal("Unexpected error building the PeerConnection. ", err)
	}
	log.Printf("new VideoTrack \n")

	// Create a video track
	videoTrack, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264}, "video", "whipnut")
	if err != nil {
		log.Println("Cant make track, ", err)
		return nil
	}
	log.Printf("add VideoTrack \n")

	if _, err := pc.AddTrack(videoTrack); err != nil {
		log.Println("Cant add track, ", err)
		return nil
	}

	pc.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		log.Printf("PeerConnection State has changed %s \n", connectionState.String())
	})

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		log.Fatal("PeerConnection could not create offer. ", err)
	}

	err = pc.SetLocalDescription(offer)
	if err != nil {
		log.Fatal("PeerConnection could not set local offer. ", err)
	}

	// Block until ICE Gathering is complete, disabling trickle ICE
	// we do this because we only can exchange one signaling message
	// in a production application you should exchange ICE Candidates via OnICECandidate
	gatherComplete := webrtc.GatheringCompletePromise(pc)
	<-gatherComplete
	log.Printf("gather completed \n")

	var sdp = []byte(pc.LocalDescription().SDP)
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: false, // original code has this as true which _has_ to be a mistake.
			},
		},
	}
	req, err := http.NewRequest("POST", whip.endpoint, bytes.NewBuffer(sdp))
	if err != nil {
		log.Fatal("Unexpected error building http request. ", err)
	} else {
		log.Printf("request sent\n")
	}

	req.Header.Add("Content-Type", "application/sdp")
	if whip.token != "" {
		req.Header.Add("Authorization", "Bearer "+whip.token)
	}

	resp, err := client.Do(req)
	if err != nil {
		log.Fatal("Failed http POST request. ", err)
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)

	if resp.StatusCode != 201 {
		log.Fatalf("Non Successful POST: %d", resp.StatusCode)
	}

	resourceUrl, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		log.Fatal("Failed to parse resource url. ", err)
	}
	base, err := url.Parse(whip.endpoint)
	if err != nil {
		log.Fatal("Failed to parse base url. ", err)
	}
	whip.resourceUrl = base.ResolveReference(resourceUrl).String()

	answer := webrtc.SessionDescription{}
	answer.Type = webrtc.SDPTypeAnswer
	answer.SDP = string(body)

	err = pc.SetRemoteDescription(answer)
	if err != nil {
		log.Fatal("PeerConnection could not set remote answer. ", err)
	} else {
		log.Printf("set answer ok \n")
	}
	return videoTrack
}

func (whip *WHIPClient) Close(skipTlsAuth bool) {
	req, err := http.NewRequest("DELETE", whip.resourceUrl, nil)
	if err != nil {
		log.Fatal("Unexpected error building http request. ", err)
	}
	req.Header.Add("Authorization", "Bearer "+whip.token)

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: skipTlsAuth,
			},
		},
	}
	_, err = client.Do(req)
	if err != nil {
		log.Fatal("Failed http DELETE request. ", err)
	}
}
