package maintenance

import (
	"fmt"
	"os"
	"syscall"
)

func directoryIdentity(path string) (string, error) {
	i, e := os.Stat(path)
	if e != nil {
		return "", e
	}
	s := i.Sys().(*syscall.Stat_t)
	return fmt.Sprintf("%v:%v", s.Dev, s.Ino), nil
}
