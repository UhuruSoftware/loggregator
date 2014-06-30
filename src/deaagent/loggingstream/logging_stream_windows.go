// +build windows

package loggingstream

import (
	"fmt"
	"github.com/cloudfoundry/loggregatorlib/logmessage"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (ls *LoggingStream) Listen() <-chan *logmessage.LogMessage {

	messageChan := make(chan *logmessage.LogMessage, 1024)

	go func() {
		defer close(messageChan)

		logFile := filepath.Join(ls.task.Identifier(), "logs", socketName(ls.messageType))

		const bufferSize int64 = 64
		buf := make([]byte, bufferSize)
		var lastOffset int64 = -1
		var lastBuffer string = ""
		var lastFileSize int64 = 0
		var lastModTime time.Time

		// wait for file to exist
		for {
			_, err := os.Stat(logFile)
			if err != nil {
				if !os.IsNotExist(err) {
					ls.logger.Infof("Error while reading from stream %s, %s, %s", ls.messageType, ls.task.Identifier(), err)
					return
				} else {
					time.Sleep(250 * time.Millisecond)
					continue
				}
				break
			}
			break
		}

		for {

			file, err := os.Open(logFile)

			if err != nil {
				ls.logger.Infof("Error while reading from stream %s, %s, %s", ls.messageType, ls.task.Identifier(), err)
				return
			}

			for {

				select {
				case <-ls.closeChan:
					break
				default:
				}

				file.Seek(lastOffset+1, 0)

				n, err := file.Read(buf)

				if err == io.EOF {
					break
				} else if err != nil {
					ls.logger.Infof("Error while reading from stream %s, %s, %s", ls.messageType, ls.task.Identifier(), err)
					return
				}

				if n > 0 {
					text := string(buf[0:n])
					lastEol := strings.LastIndex(text, "\n")

					goldenString := ""

					if lastEol != -1 {
						goldenString = lastBuffer + text[0:lastEol]
						lastBuffer = ""
					}

					lastBuffer += text[lastEol+1 : n]

					if len(lastBuffer) > 10*1024 {
						lastBuffer = lastBuffer[10*1024 : len(lastBuffer)]
						msg := fmt.Sprintf("Line buffer longer than 10KB; truncating")
						messageChan <- ls.newLogMessage([]byte(msg))
					}

					if goldenString != "" {
						messageChan <- ls.newLogMessage([]byte(goldenString))
					}

					lastOffset += int64(n)
				} else {
					break
				}
				if int64(n) < bufferSize {
					break
				}
			}

			file.Close()

			var retry int = 0
			const permissionDeniedRetryCount int = 5

			// wait until file was changed
			for {
				select {
				case <-ls.closeChan:
					return
				default:
				}

				time.Sleep(200 * time.Millisecond)
				fi, err := os.Stat(logFile)
				if err != nil {
					if os.IsPermission(err) && retry < permissionDeniedRetryCount {
						retry++
						continue
					}
					ls.Stop()
					return
				}

				if fi.ModTime() != lastModTime || lastFileSize != fi.Size() {
					lastModTime = fi.ModTime()
					break
				}

				if lastFileSize > fi.Size() {
					// file was truncated
					lastOffset = 0
					lastBuffer = ""
					break
				}
			}
		}
	}()

	return messageChan
}

func socketName(messageType logmessage.LogMessage_MessageType) string {
	if messageType == logmessage.LogMessage_OUT {
		return "stdout.log"
	}
	return "stderr.log"
}
