package deaagent_test

import (
	"deaagent/domain"
	"github.com/cloudfoundry/loggregatorlib/emitter"
	"github.com/cloudfoundry/loggregatorlib/logmessage"
	"net"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"testing"
)

func TestDeaagent(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Deaagent Suite")
}

const SOCKET_PREFIX = "\n\n\n\n"

type MockLoggregatorEmitter struct {
	received chan *logmessage.LogMessage
}

func (m MockLoggregatorEmitter) Emit(a, b string) {

}

func (m MockLoggregatorEmitter) EmitError(a, b string) {

}

func (m MockLoggregatorEmitter) EmitLogMessage(message *logmessage.LogMessage) {
	m.received <- message
}

func setupEmitter() (emitter.Emitter, chan *logmessage.LogMessage) {
	mockLoggregatorEmitter := new(MockLoggregatorEmitter)
	mockLoggregatorEmitter.received = make(chan *logmessage.LogMessage)
	return mockLoggregatorEmitter, mockLoggregatorEmitter.received
}

func setupTaskSockets(task *domain.Task) (stdout net.Listener, stderr net.Listener) {
	os.MkdirAll(task.Identifier(), 0777)
	stdoutSocketPath := filepath.Join(task.Identifier(), "stdout.sock")
	os.Remove(stdoutSocketPath)
	stdoutListener, _ := net.Listen("unix", stdoutSocketPath)

	stderrSocketPath := filepath.Join(task.Identifier(), "stderr.sock")
	os.Remove(stderrSocketPath)
	stderrListener, _ := net.Listen("unix", stderrSocketPath)

	return stdoutListener, stderrListener
}
