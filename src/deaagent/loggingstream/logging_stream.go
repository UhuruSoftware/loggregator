package loggingstream

import (
	"code.google.com/p/gogoprotobuf/proto"
	"deaagent/domain"
	"fmt"
	"github.com/cloudfoundry/gosteno"
	"github.com/cloudfoundry/loggregatorlib/cfcomponent/instrumentation"
	"github.com/cloudfoundry/loggregatorlib/logmessage"
	"net"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type LoggingStream struct {
	connection       net.Conn
	task             *domain.Task
	logger           *gosteno.Logger
	messageType      logmessage.LogMessage_MessageType
	messagesReceived uint64
	bytesReceived    uint64
	closeChan        chan struct{}
	sync.Mutex
}

func NewLoggingStream(task *domain.Task, logger *gosteno.Logger, messageType logmessage.LogMessage_MessageType) (ls *LoggingStream) {
	return &LoggingStream{task: task, logger: logger, messageType: messageType, closeChan: make(chan struct{})}
}

func (ls *LoggingStream) setConnection(connection net.Conn) {
	ls.Lock()
	defer ls.Unlock()
	ls.connection = connection
}

func (ls *LoggingStream) connect() (net.Conn, error) {
	var err error
	var connection net.Conn
	for i := 0; i < 10; i++ {
		connection, err = net.Dial("unix", filepath.Join(ls.task.Identifier(), socketName(ls.messageType)))
		if err == nil {
			ls.logger.Debugf("Opened socket %s, %s", ls.messageType, ls.task.Identifier())
			break
		}
		ls.logger.Debugf("Could not read from socket %s, %s, retrying: %s", ls.messageType, ls.task.Identifier(), err)
		time.Sleep(100 * time.Millisecond)
	}
	return connection, err
}

func (ls *LoggingStream) Emit() instrumentation.Context {
	return instrumentation.Context{Name: "loggingStream:" + ls.task.WardenContainerPath + " type " + socketName(ls.messageType),
		Metrics: []instrumentation.Metric{
			instrumentation.Metric{Name: "receivedMessageCount", Value: atomic.LoadUint64(&ls.messagesReceived)},
			instrumentation.Metric{Name: "receivedByteCount", Value: atomic.LoadUint64(&ls.bytesReceived)},
		},
	}
}

func (ls *LoggingStream) Stop() {
	ls.Lock()
	defer ls.Unlock()

	select {
	case <-ls.closeChan:
	default:
		close(ls.closeChan)
	}

	if ls.connection != nil {
		ls.connection.Close()
	}
	ls.logger.Infof("Stopped reading from socket %s, %s", ls.messageType, ls.task.Identifier())
}

func (ls *LoggingStream) newLogMessage(message []byte) *logmessage.LogMessage {
	currentTime := time.Now()
	sourceName := ls.task.SourceName
	sourceId := strconv.FormatUint(ls.task.Index, 10)
	messageCopy := make([]byte, len(message))
	copyCount := copy(messageCopy, message)
	if copyCount != len(message) {
		panic(fmt.Sprintf("Didn't copy the message %d, %s", copyCount, message))
	}
	return &logmessage.LogMessage{
		Message:     messageCopy,
		AppId:       proto.String(ls.task.ApplicationId),
		DrainUrls:   ls.task.DrainUrls,
		MessageType: &ls.messageType,
		SourceName:  &sourceName,
		SourceId:    &sourceId,
		Timestamp:   proto.Int64(currentTime.UnixNano()),
	}
}
