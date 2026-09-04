// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"os"
	"os/exec"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/log"
)

func RunHooks(job config.JobConfig, action, kind, dbName string, extra map[string]string) {
	if job.Hooks == nil {
		return
	}
	var hooks []string
	switch action {
	case "pre":
		hooks = job.Hooks.Pre
	case "post":
		hooks = job.Hooks.Post
	}
	if len(hooks) == 0 {
		return
	}
	for _, h := range hooks {
		cmd := exec.Command("/bin/sh", "-c", h)
		env := os.Environ()
		env = append(env,
			"DBB_JOB="+job.Name,
			"DBB_ACTION="+action,
			"DBB_KIND="+kind,
			"DBB_TYPE="+job.Type,
			"DBB_HOST="+job.Host,
			"DBB_NAME="+dbName,
		)
		for k, v := range extra {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			JLog(log.LevelWarn, job, "hook failed",
				"status", "warn", "kind", kind, "action", action, "script", h, "error", err.Error())
		} else {
			JLog(log.LevelDebug, job, "hook completed",
				"status", "debug", "kind", kind, "action", action, "script", h)
		}
	}
}
