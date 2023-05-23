
<img src="https://user-images.githubusercontent.com/244265/200068510-7c24d5c7-6ba0-44ee-8e60-0f157f990b90.png" width="350" />

donut is a zero setup required SRT+MPEG-TS -> WebRTC Bridge powered by [Pion](http://pion.ly/).
This fork operates in 2 possible modes:
1) as a viewer - providing a browser compatible endpoint for a single SRT stream. - useful for previews and testing.
2) as a bridge - providing a protocol translator to WHIP  - useful for feeding SRT into SFUs or broadcast fanout systems

### Install & Run Locally

Make sure you have the `libsrt` installed in your system. If not, follow their [build instructions](https://github.com/Haivision/srt#build-instructions). 
Once you finish installing it, execute:

```
$ go install github.com/flavioribeiro/donut@latest
```
Once installed, execute `donut`. This will be in your `$GOPATH/bin`. The default will be `~/go/bin/donut`

Run without args it works in viewer mode and springs up a webserver
With -whip-uri and -srt-uri set it acts as a WHIP forwarder

### Install & Run using Docker

Alternatively, you can build a docker image. Docker will take care of downloading the dependencies (including the libsrt) and compiling donut for you.

```
$ docker build -t donut .
$ docker run -it -p 8080:8080 donut
```

### Open the Web UI for viewer
Open [http://localhost:8080](http://localhost:8080). You will see three text boxes. Fill in your details for your SRT listener configuration and hit connect.

### Acting as a WHIP forwarder 
This example requires port 5000 UDP to be open for inbound traffic 
```
$ donut ./donut -whip-uri https://galene.pi.pe/group/whip/ -srt-uri "srt://0.0.0.0:5000?streamid=test" \
    -whip-token ${bearertoken}
```
You can then send _multiple_ SRT streams to be forwarded as WHIP
examples: Camera from a mac:
```
ffmpeg -f avfoundation  -framerate 30 -i "0" \
   -pix_fmt yuv420p -c:v libx264 -b:v 1000k -g 30 -keyint_min 120 -profile:v baseline \
   -preset veryfast -f mpegts  "srt://${bridgeIP}:5000?streamid=me"
```
Or from a raspi 
```
ffmpeg -f video4linux2 -input_format h264 -video_size 1280x720 -framerate 30 \
-i /dev/video0 -vcodec copy -an -f mpegts "srt://${bridgeIP}:5000?streamid=picam"


```