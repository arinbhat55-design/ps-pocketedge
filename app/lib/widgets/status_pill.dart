import 'package:flutter/material.dart';

import '../models/server.dart';
import '../theme/app_theme.dart';

enum StatusTone {
  healthy(AppColors.healthy),
  warning(AppColors.warning),
  failed(AppColors.failed),
  neutral(AppColors.neutral);

  final Color color;
  const StatusTone(this.color);
}

/// A status as text + tone, so color is never the only signal.
typedef StatusLabel = ({String label, StatusTone tone});

/// Docker container state → human label. "exited" reads as Stopped (it's
/// the normal result of `docker stop`), not as a failure; "dead" is the
/// real failure state.
StatusLabel containerStatus(String state) => switch (state) {
  'running' => (label: 'Running', tone: StatusTone.healthy),
  'paused' => (label: 'Paused', tone: StatusTone.warning),
  'restarting' => (label: 'Restarting', tone: StatusTone.warning),
  'exited' => (label: 'Stopped', tone: StatusTone.neutral),
  'created' => (label: 'Created', tone: StatusTone.neutral),
  'removing' => (label: 'Removing', tone: StatusTone.neutral),
  'dead' => (label: 'Failed', tone: StatusTone.failed),
  _ => (label: _capitalize(state), tone: StatusTone.neutral),
};

/// [containerStatus] refined with Docker's status line ("Up 3 hours
/// (unhealthy)"), so a running container failing its health check isn't
/// shown as plain healthy.
StatusLabel containerStatusDetailed(String state, String? status) {
  final s = status?.toLowerCase() ?? '';
  if (state == 'running') {
    if (s.contains('(unhealthy)')) {
      return (label: 'Unhealthy', tone: StatusTone.warning);
    }
    if (s.contains('health: starting')) {
      return (label: 'Starting', tone: StatusTone.warning);
    }
  }
  return containerStatus(state);
}

/// How long without a heartbeat before a server counts as disconnected.
/// Agents beat every 20 s (60 s on constrained hosts), so this allows a
/// few missed beats before alarming.
const serverStaleAfter = Duration(minutes: 3);

/// Server connectivity as the user should read it. The control plane only
/// records `online` on each heartbeat and never flips it back, so a server
/// whose agent has gone quiet is detected here from [Server.lastHeartbeatAt].
StatusLabel serverStatus(Server server, {DateTime? now}) => serverStatusFrom(
  status: server.status,
  lastHeartbeatAt: server.lastHeartbeatAt,
  now: now,
);

/// [serverStatus] from raw fields, for screens that track the latest
/// heartbeat themselves (e.g. from the live stream).
StatusLabel serverStatusFrom({
  required String status,
  required DateTime? lastHeartbeatAt,
  DateTime? now,
}) {
  if (status != 'online') {
    return (label: _capitalize(status), tone: StatusTone.failed);
  }
  if (lastHeartbeatAt == null) {
    return (label: 'Waiting for agent', tone: StatusTone.neutral);
  }
  final age = (now ?? DateTime.now()).difference(lastHeartbeatAt);
  if (age > serverStaleAfter) {
    return (label: 'Disconnected', tone: StatusTone.failed);
  }
  return (label: 'Online', tone: StatusTone.healthy);
}

bool isServerOnline(Server server, {DateTime? now}) =>
    serverStatus(server, now: now).tone == StatusTone.healthy;

/// Resource usage (%) → tone: amber from 75 %, red from 90 %. Judged on
/// the whole-number value the UI shows, so "75%" is never shown as normal.
StatusTone usageTone(double percent) {
  final shown = percent.round();
  return shown >= 90
      ? StatusTone.failed
      : shown >= 75
      ? StatusTone.warning
      : StatusTone.healthy;
}

String _capitalize(String s) =>
    s.isEmpty ? 'Unknown' : s[0].toUpperCase() + s.substring(1);

/// A small rounded status label: tinted background, colored dot, and the
/// status in words. Easier to scan than a colored icon, and readable
/// without color.
class StatusPill extends StatelessWidget {
  final String label;
  final StatusTone tone;

  const StatusPill({super.key, required this.label, required this.tone});

  StatusPill.of(StatusLabel status, {Key? key})
    : this(key: key, label: status.label, tone: status.tone);

  @override
  Widget build(BuildContext context) {
    final color = tone.color;
    return Semantics(
      label: 'Status: $label',
      excludeSemantics: true,
      child: Container(
        padding: const EdgeInsets.fromLTRB(Space.sm, 2, Space.sm + 2, 2),
        decoration: BoxDecoration(
          color: color.withValues(alpha: 0.12),
          borderRadius: BorderRadius.circular(999),
          border: Border.all(color: color.withValues(alpha: 0.35)),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Container(
              width: 6,
              height: 6,
              decoration: BoxDecoration(color: color, shape: BoxShape.circle),
            ),
            const SizedBox(width: 6),
            Flexible(
              child: Text(
                label,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: Theme.of(context).textTheme.labelSmall?.copyWith(
                  color: Color.lerp(color, Colors.white, 0.25),
                  fontWeight: FontWeight.w600,
                  letterSpacing: 0.2,
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}
