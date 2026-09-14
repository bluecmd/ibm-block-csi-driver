//go:build windows

/**
 * Copyright 2026 Christian Svensson.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package driver

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/ibm/ibm-block-csi-driver/node/logger"
)

const (
	powershellCmd     = "powershell.exe"
	powershellTimeout = 60 * time.Second

	// powershellPreamble silences progress records (which PowerShell would
	// otherwise serialise as CLIXML when it has no console) and makes
	// cmdlet errors terminate the script with a non-zero exit code.
	powershellPreamble = "$ProgressPreference = 'SilentlyContinue'; $ErrorActionPreference = 'Stop'; "
)

// Powershell runs scripts with the host's Windows PowerShell.
type Powershell struct{}

func (p *Powershell) Run(script string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), powershellTimeout)
	defer cancel()

	logger.Debugf("Executing powershell script : {%v}", script)
	cmd := exec.CommandContext(ctx, powershellCmd, "-NoLogo", "-NonInteractive", "-NoProfile", "-Command", powershellPreamble+script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		logger.Debugf("Powershell script timeout reached")
		return "", ctx.Err()
	}
	out := strings.TrimSpace(stdout.String())
	if err != nil {
		errText := strings.TrimSpace(stderr.String())
		logger.Debugf("Powershell script failed: %v: %s", err, errText)
		return out, fmt.Errorf("powershell failed: %w: %s", err, errText)
	}
	logger.Debugf("Output from powershell script: %s", out)
	return out, nil
}
