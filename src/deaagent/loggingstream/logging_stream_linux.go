// +build !windows

package loggingstream

import (
	"bufio"
	"fmt"
	"github.com/cloudfoundry/loggregatorlib/logmessage"
	"sync/atomic"
)

func (ls *LoggingStream) Listen() <-chan *logmessage.LogMessage {

	messageChan := make(chan *logmessage.LogMessage, 1024)

	go func() {
		defer close(messageChan)

		connection, err := ls.connect()
		if err != nil {
			ls.logger.Infof("Error while reading from socket %s, %s, %s", ls.messageType, ls.task.Identifier(), err)
			return
		}
		ls.setConnection(connection)

		for {
			select {
			case <-ls.closeChan:
				return
			default:
			}

			scanner := bufio.NewScanner(connection)
			for scanner.Scan() {
				line := scanner.Bytes()
				readCount := len(line)
				if readCount < 1 {
					continue
				}
				ls.logger.Debugf("Read %d bytes from task socket %s, %s", readCount, ls.messageType, ls.task.Identifier())
				atomic.AddUint64(&ls.messagesReceived, 1)
				atomic.AddUint64(&ls.bytesReceived, uint64(readCount))

				messageChan <- ls.newLogMessage(line)

				ls.logger.Debugf("Sent %d bytes to loggregator client from %s, %s", readCount, ls.messageType, ls.task.Identifier())
			}
			err = scanner.Err()

			if err != nil {
				ls.logger.Infof(fmt.Sprintf("Error while reading from socket %s, %s, %s", ls.messageType, ls.task.Identifier(), err))

				messageChan <- ls.newLogMessage([]byte(fmt.Sprintf("Dropped a message because of read error: %s", err)))
				continue
			}

			ls.logger.Debugf("EOF while reading from socket %s, %s", ls.messageType, ls.task.Identifier())
			return
		}
	}()

	return messageChan
}

func socketName(messageType logmessage.LogMessage_MessageType) string {
	if messageType == logmessage.LogMessage_OUT {
		return "stdout.sock"
	}
	return "stderr.sock"
}
