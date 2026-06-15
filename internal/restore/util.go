package restore

import (
	"fmt"
	"io"
)

func ioReader(value any) (io.Reader, error) {
	if value == nil {
		return nil, nil
	}

	reader, ok := value.(io.Reader)
	if !ok {
		return nil, fmt.Errorf(
			"invalid command input type %T: expected io.Reader",
			value,
		)
	}

	return reader, nil
}