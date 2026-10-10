-- Add per-host transport selection for reaching a remote Docker daemon over ssh.
--
--   'forward'    (new default) forwards the remote Docker unix socket over an ssh
--                local-forward and talks to it locally. It needs only socket
--                access + ssh on the remote — no `docker` binary on the remote's
--                non-interactive PATH (works on restrictive hosts like Synology).
--   'dial-stdio' (Phase 1)     runs `ssh … docker system dial-stdio`, which
--                requires the docker binary on the remote's SSH PATH.
--
-- remote_socket is the path to the Docker socket on the remote host, forwarded by
-- the 'forward' transport. Both columns are portable across sqlite and postgres
-- (plain ADD COLUMN with a NOT NULL DEFAULT).
ALTER TABLE hosts ADD COLUMN transport TEXT NOT NULL DEFAULT 'forward';
ALTER TABLE hosts ADD COLUMN remote_socket TEXT NOT NULL DEFAULT '/var/run/docker.sock';
