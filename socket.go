package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/taktod/ttLibGo/ttLibGo"
	"github.com/taktod/ttLibGo/ttLibGoFdkaac"
	"github.com/taktod/ttLibGo/ttLibGoFfmpeg"
	"github.com/taktod/ttLibGo/ttLibGoX264"
	"golang.org/x/net/websocket"
)

func publishHandler(ws *websocket.Conn) {
	// このhandlerは接続ごとに生成されるみたい
	// MediaRecorderで生成されるファイルは基本webmなので、matroskaデータで受け取る方向でつくっておく
	var reader ttLibGo.Reader
	reader.Init("mkv")
	defer reader.Close()
	// 映像のdecoder ffmpegのavcodecで実行
	var videoDecoder ttLibGoFfmpeg.AvcodecDecoder
	defer videoDecoder.Close()
	// 音声のdecoder ffmpegのavcodecで実行
	var audioDecoder ttLibGoFfmpeg.AvcodecDecoder
	defer audioDecoder.Close()
	// 映像のencoder x264をつかってh264にする
	var videoEncoder ttLibGoX264.X264Encoder
	defer videoEncoder.Close()
	// 音声のencoder fdkaacを使ってaacにする
	var audioEncoder ttLibGoFdkaac.FdkaacEncoder
	defer audioEncoder.Close()
	// avcodecで音声をdecodeするとpcmF32になるので、pcmS16にしたい
	// 音声のresampler ffmpegのswresampleで実行
	var audioResampler ttLibGoFfmpeg.SwresampleResampler
	defer audioResampler.Close()
	// 生成データの書き出し fragmented mp4で書き出しておく
	var writer ttLibGo.Writer
	writer.Init("mp4", 1000, "h264", "aac") // 映像はh264で id = 1、音声はaacでid = 2に指定
	writer.Mode = 0x20                      // x264の影響でbフレームがはいるのでdts考慮状態で書き出しを実施する
	defer writer.Close()
	// 受け取ったオリジナルデータをファイルに書き出す
	outOriginal, err := os.OpenFile("test_original.webm", os.O_CREATE|os.O_WRONLY, 0666)
	if err != nil {
		panic(err)
	}
	// 作成したmp4データをファイルに書き出す
	out, err := os.OpenFile("test.mp4", os.O_CREATE|os.O_WRONLY, 0666)
	if err != nil {
		panic(err)
	}
	// 開始時に音声のptsが0から始まらないことがあるみたいなので、データとして持っとく
	var firstAudioPts float32
	firstAudioPts = -1
	// フレーム書き出し処理
	var writeFrame = func(frame *ttLibGo.Frame) bool {
		// fmt.Println(frame)
		return writer.WriteFrame(frame, func(data []byte) bool {
			// fmt.Println(data)
			if out != nil {
				out.Write(data)
			}
			return true
		})
	}
	for {
		// とりあえず65kbyteごと処理する
		buf := make([]byte, 65536)
		length, err := ws.Read(buf)
		if err != nil {
			// なにかエラーでたら、終わる
			break
		}
		if length == 0 {
			// 取得データが0の時も終わる
			break
		}
		// binaryからフレームを取り出す
		if !reader.ReadFrame(
			buf,
			uint64(length),
			func(frame *ttLibGo.Frame) bool {
				if frame.ID >= 3 {
					// 2トラック以上のデータは扱いません
					// あと同じcodecが2トラックくることも考慮しません。
					return true
				}
				// 取得フレームのcodecTypeで処理分岐
				switch frame.CodecType {
				case "aac":
					// aacの場合はそのまま書き出しに回す
					frame.ID = 2
					return writeFrame(frame)
				case "mp3":
					fallthrough
				case "opus":
					fallthrough
				case "vorbis":
					// mp3 opus vorbisの場合は、初めのフレームのpts値を保存
					if firstAudioPts == -1 {
						firstAudioPts = float32(frame.Pts) / float32(frame.Timebase)
					}
					// decoderを初期化 ２度目以降は内部で処理をスキップする
					audioDecoder.InitAudio(frame.CodecType, frame.SampleRate, frame.ChannelNum)
					// デコード実施
					return audioDecoder.Decode(frame, func(frame *ttLibGo.Frame) bool {
						// エンコード実施する関数
						var doEncode = func(frame *ttLibGo.Frame) bool {
							// エンコーダーの初期化 low profileで実行する 96kbpsあたりで
							audioEncoder.Init("AOT_AAC_LC", frame.SampleRate, frame.ChannelNum, 96000)
							return audioEncoder.Encode(frame, func(frame *ttLibGo.Frame) bool {
								// できあがったらptsを初期のズレ分考慮しつつ書き出し処理にまわす
								frame.ID = 2
								frame.Pts += uint64(firstAudioPts * float32(frame.Timebase))
								return writeFrame(frame)
							})
						}
						if frame.CodecType == "pcmF32" {
							// floatの場合はresampleかける
							audioResampler.Init(frame.CodecType, frame.SubType, frame.SampleRate, frame.ChannelNum,
								"pcmS16", "littleEndian", frame.SampleRate, frame.ChannelNum)
							return audioResampler.Resample(frame, doEncode)
						}
						// floatじゃない場合はそのままencodeに回す
						return doEncode(frame)
					})
				case "h264":
					// h264のデータもそのまま書き出しにまわしてもよかったんだけど
					// keyFrame間隔が非常に長くなって、なかなか書き出しが実施されない懸念があるので
					// encodeし直すことにする
					if frame.SubType == "" {
						// subtypeがconfigDataやslice sliceIDRでないものをdecoderに渡すと
						// falseを応答するので、処理にまわさない
						return true
					}
					fallthrough
				case "vp8":
					fallthrough
				case "vp9":
					// 初期化
					videoDecoder.InitVideo(frame.CodecType, frame.Width, frame.Height)
					// デコード実施
					return videoDecoder.Decode(frame, func(frame *ttLibGo.Frame) bool {
						// エンコーダー初期化
						videoEncoder.Init(frame.Width, frame.Height,
							"superfast", "main", "zerolatency",
							map[string]string{
								"open-gop":    "1",
								"threads":     "1",
								"merange":     "16",
								"qcomp":       "0.6",
								"ip-factor":   "0.71",
								"bitrate":     "600",
								"qp":          "21",
								"crf":         "23",
								"crf-max":     "23",
								"fps":         "30/1",
								"keyint":      "15",
								"keyint-min":  "15",
								"bframes":     "3",
								"vbv-maxrate": "0",
								"vbv-bufsize": "1024",
								"qp-max":      "40",
								"qp-min":      "21",
								"qp-step":     "4"})
						// エンコード実施
						return videoEncoder.Encode(frame, func(frame *ttLibGo.Frame) bool {
							frame.ID = 1
							// データができたら、書き出しにまわす
							return writeFrame(frame)
						})
					})
				}
				return true
			}) {
			fmt.Println("frame読み込み失敗")
			break
		}
		// オリジナルのデータは65536byteではないので、sliceして一部だけ書き出すようにする
		outOriginal.Write(buf[0:length])
	}
}

func main() {
	// ws://xxx:8080/publishでアクセス可能にする
	http.HandleFunc("/publish",
		func(w http.ResponseWriter, req *http.Request) {
			s := websocket.Server{Handler: websocket.Handler(publishHandler)}
			s.ServeHTTP(w, req)
		})
	http.Handle("/", http.FileServer(http.Dir("./")))
	if err := http.ListenAndServe(":8080", nil); err != nil {
		panic("ListenAndServe: " + err.Error())
	}
}
