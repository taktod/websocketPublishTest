# websocketPublishTest

# 作者

taktod
twitter: https://twitter.com/taktod
email: poepoemix@hotmail.com

# 概要

ttLibGo https://github.com/taktod/ttLibGo
を書いたので、それを利用してプログラム組んでみました。

html5で
getUserMedia -> mediaRecorder -> websocketでデータを送付

goで
websocketでデータ取得 -> readerでwebm解析 -> h264とaacにencode -> fragmented mp4で書き出し

としてます。

html5のブラウザだけで、高画質な配信できるのは便利ですね。