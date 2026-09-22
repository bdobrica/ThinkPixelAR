package agentd

import "errors"

var ErrProcessClosed = errors.New("harness supervisor is shutting down")
