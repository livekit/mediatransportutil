package main

import (
	"context"
	"fmt"
	"time"

	"github.com/pkg/errors"

	"github.com/livekit/mediatransportutil/pkg/rtcconfig"
)

func main() {
	stunServers := rtcconfig.DefaultStunServers
	var err error
	for i := 0; i < 3; i++ {
		var ip string
		ip, err = rtcconfig.GetExternalIP(context.Background(), stunServers, nil)
		if err == nil {
			fmt.Println(ip)
			// return ip, nil
		} else {
			time.Sleep(500 * time.Millisecond)
		}
	}
	fmt.Println(errors.Errorf("could not resolve external IP: %v", err))
	// return "", errors.Errorf("could not resolve external IP: %v", err)
}
