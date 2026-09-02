import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/container.dart';
import '../../models/image.dart';
import '../../models/server_metrics.dart';
import 'clone_container_dialog.dart';
import 'container_schedules_screen.dart';
import 'recreate_container_dialog.dart';
import 'rename_container_dialog.dart';
import 'restart_policy_dialog.dart';

/// Full detail for one container: the cheap [ContainerInfo] fields (already
/// known from the list/stream, shown immediately) plus the expensive
/// [ContainerDetail] fields (env vars, restart policy, health), fetched on
/// demand from the owning agent via a live ContainerInspect. Also hosts the
/// full container-management action menu: lifecycle actions, rename,
/// clone, recreate, restart policy, and schedules.
class ContainerDetailScreen extends StatefulWidget {
  final ApiClient apiClient;
  final String serverId;
  final String serverName;
  final ContainerInfo container;

  const ContainerDetailScreen({
    super.key,
    required this.apiClient,
    required this.serverId,
    required this.serverName,
    required this.container,
  });

  @override
  State<ContainerDetailScreen> createState() => _ContainerDetailScreenState();
}

class _ContainerDetailScreenState extends State<ContainerDetailScreen> {
  late Future<ContainerDetail> _detailFuture;
  late final String _currentName = widget.container.name;
  List<ImageRollbackEntry> _rollbackHistory = [];
  bool _busy = false;
  // True once any action below has actually changed something server-side
  // (start/stop/rename/remove/recreate/...) — popped back to the caller so
  // the fleet-wide/per-server list knows to refresh instead of showing
  // stale state.
  bool _changed = false;

  @override
  void initState() {
    super.initState();
    _detailFuture = widget.apiClient.inspectContainer(
      widget.serverId,
      widget.container.containerId,
    );
    _loadRollbackHistory();
  }

  Future<void> _loadRollbackHistory() async {
    try {
      final history = await widget.apiClient.listImageRollbackHistory(
        widget.serverId,
        widget.container.containerId,
      );
      if (mounted) setState(() => _rollbackHistory = history);
    } catch (_) {
      // Best-effort — the "Rollback" button just stays hidden.
    }
  }

  Future<void> _rollback() async {
    if (_rollbackHistory.isEmpty) return;
    final previousImage = _rollbackHistory.first.previousImage;
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (_) => AlertDialog(
        title: const Text('Rollback to previous image?'),
        content: Text(
          'Recreates this container on its previous image:\n\n$previousImage\n\n'
          'Every other setting (name, ports, volumes, restart policy) stays the same.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.of(context).pop(true),
            child: const Text('Rollback'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;

    setState(() => _busy = true);
    try {
      final result = await widget.apiClient.rollbackContainer(
        widget.serverId,
        widget.container.containerId,
      );
      if (!mounted) return;
      if (!result.success) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text(result.error ?? 'Rollback failed')));
        return;
      }
      // Recreate mints a new container id — this screen is built around the
      // old one, so pop back and let the caller refresh, same as _recreate.
      Navigator.of(context).pop(true);
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Rollback failed: $e')));
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  void _refreshDetail() {
    setState(() {
      _detailFuture = widget.apiClient.inspectContainer(
        widget.serverId,
        widget.container.containerId,
      );
    });
  }

  String _uptime(DateTime? createdAt) {
    if (createdAt == null) return '—';
    final d = DateTime.now().difference(createdAt);
    if (d.inDays > 0) return '${d.inDays}d ${d.inHours % 24}h';
    if (d.inHours > 0) return '${d.inHours}h ${d.inMinutes % 60}m';
    return '${d.inMinutes}m';
  }

  Future<void> _runAction(String action, {bool force = false}) async {
    setState(() => _busy = true);
    try {
      final result = await widget.apiClient.containerAction(
        widget.serverId,
        widget.container.containerId,
        action,
        force: force,
      );
      if (!mounted) return;
      if (!result.success) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(result.error ?? 'Action failed')),
        );
        return;
      }
      _changed = true;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(SnackBar(content: Text('$action succeeded')));
      if (action == 'remove') {
        Navigator.of(context).pop(true);
        return;
      }
      _refreshDetail();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Failed to $action container: $e')),
        );
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _confirmAndRun(String action, {bool force = false}) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (_) => AlertDialog(
        title: Text(
          action == 'kill' ? 'Force-kill container?' : 'Remove container?',
        ),
        content: Text(
          action == 'kill'
              ? 'Sends SIGKILL immediately, with no graceful shutdown. Use for an '
                    'unresponsive container.'
              : 'This permanently deletes the container'
                    '${force ? ' (forced — it will be stopped first if running)' : ''}. '
                    'Any data outside a named volume is lost.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(backgroundColor: Colors.red),
            onPressed: () => Navigator.of(context).pop(true),
            child: Text(action == 'kill' ? 'Force-kill' : 'Remove'),
          ),
        ],
      ),
    );
    if (confirmed == true) _runAction(action, force: force);
  }

  Future<void> _rename() async {
    final ok = await showRenameContainerDialog(
      context,
      apiClient: widget.apiClient,
      serverId: widget.serverId,
      containerId: widget.container.containerId,
      currentName: _currentName,
    );
    if (ok == true) {
      // The controller that opened this screen (list) knows the new name
      // came from a fresh GET; here we just don't have one, so the rename
      // dialog's own submitted text becomes the new display name is not
      // available — reload via inspect isn't enough (name isn't part of
      // ContainerDetail). Simplest correct fix: tell the caller to refresh
      // and pop, since this screen has no server-authoritative way to know
      // the new name beyond re-fetching the list itself.
      _changed = true;
      if (mounted) Navigator.of(context).pop(true);
    }
  }

  Future<void> _clone() async {
    final newId = await showCloneContainerDialog(
      context,
      apiClient: widget.apiClient,
      serverId: widget.serverId,
      containerId: widget.container.containerId,
      currentName: _currentName,
    );
    if (newId != null && mounted) {
      _changed = true;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('Container cloned')));
    }
  }

  Future<void> _recreate(ContainerDetail detail) async {
    final current = ContainerConfig(
      image: widget.container.image ?? '',
      name: _currentName,
      env: detail.env,
      ports: [
        for (final p in widget.container.ports)
          if (p.privatePort != 0)
            ContainerPortSpec(
              containerPort: p.privatePort,
              hostPort: p.publicPort,
              protocol: p.type.isEmpty ? 'tcp' : p.type,
            ),
      ],
      restartPolicyName: detail.restartPolicyName.isEmpty
          ? 'no'
          : detail.restartPolicyName,
      restartPolicyMaxRetryCount: detail.restartPolicyMaxRetryCount,
    );
    final newId = await showRecreateContainerDialog(
      context,
      apiClient: widget.apiClient,
      serverId: widget.serverId,
      containerId: widget.container.containerId,
      current: current,
    );
    if (newId != null && mounted) {
      // The container id changed, so this screen (built around the old
      // id) can no longer act on it — pop back so the caller refreshes.
      Navigator.of(context).pop(true);
    }
  }

  Future<void> _restartPolicy(ContainerDetail detail) async {
    final ok = await showRestartPolicyDialog(
      context,
      apiClient: widget.apiClient,
      serverId: widget.serverId,
      containerId: widget.container.containerId,
      currentPolicyName: detail.restartPolicyName.isEmpty
          ? 'no'
          : detail.restartPolicyName,
      currentMaxRetryCount: detail.restartPolicyMaxRetryCount,
    );
    if (ok == true) {
      _changed = true;
      _refreshDetail();
    }
  }

  Future<void> _openSchedules() async {
    await Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => ContainerSchedulesScreen(
          apiClient: widget.apiClient,
          serverId: widget.serverId,
          containerId: widget.container.containerId,
          containerName: _currentName,
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final c = widget.container;
    return PopScope(
      // Blocks the default back gesture/button pop only when something
      // changed, so we can substitute a pop that carries `true` — the
      // caller (list screens) uses that to know it should refresh instead
      // of showing now-stale state.
      canPop: !_changed,
      onPopInvokedWithResult: (didPop, result) {
        if (!didPop) Navigator.of(context).pop(true);
      },
      child: Scaffold(
        appBar: AppBar(
          title: Text(_currentName),
          actions: [
            if (_busy)
              const Padding(
                padding: EdgeInsets.all(16),
                child: SizedBox(
                  width: 18,
                  height: 18,
                  child: CircularProgressIndicator(strokeWidth: 2),
                ),
              ),
            PopupMenuButton<String>(
              tooltip: 'Actions',
              enabled: !_busy,
              onSelected: (action) async {
                switch (action) {
                  case 'start':
                  case 'stop':
                  case 'restart':
                  case 'pause':
                  case 'resume':
                    _runAction(action);
                  case 'kill':
                    _confirmAndRun('kill');
                  case 'remove':
                    _confirmAndRun('remove', force: true);
                  case 'rename':
                    _rename();
                  case 'clone':
                    _clone();
                  case 'schedules':
                    _openSchedules();
                }
              },
              itemBuilder: (context) => const [
                PopupMenuItem(value: 'start', child: Text('Start')),
                PopupMenuItem(value: 'stop', child: Text('Stop')),
                PopupMenuItem(value: 'restart', child: Text('Restart')),
                PopupMenuItem(value: 'pause', child: Text('Pause')),
                PopupMenuItem(value: 'resume', child: Text('Resume')),
                PopupMenuItem(value: 'kill', child: Text('Force-kill')),
                PopupMenuItem(value: 'remove', child: Text('Remove')),
                PopupMenuDivider(),
                PopupMenuItem(value: 'rename', child: Text('Rename')),
                PopupMenuItem(value: 'clone', child: Text('Clone')),
                PopupMenuItem(value: 'schedules', child: Text('Schedules')),
              ],
            ),
          ],
        ),
        body: ListView(
          padding: const EdgeInsets.all(16),
          children: [
            if (c.deploymentId != null)
              Card(
                color: Theme.of(context).colorScheme.surfaceContainerHighest,
                child: const Padding(
                  padding: EdgeInsets.all(12),
                  child: Row(
                    children: [
                      Icon(Icons.info_outline, size: 18),
                      SizedBox(width: 8),
                      Expanded(
                        child: Text(
                          'This container belongs to a deployed stack. Lifecycle '
                          "actions here act on the container directly and won't "
                          "update the stack's desired configuration.",
                          style: TextStyle(fontSize: 12),
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            if (c.deploymentId != null) const SizedBox(height: 16),
            Row(
              children: [
                Icon(
                  Icons.circle,
                  size: 10,
                  color: containerStateColor(c.state),
                ),
                const SizedBox(width: 8),
                Text(c.status?.isNotEmpty == true ? c.status! : c.state),
              ],
            ),
            const SizedBox(height: 16),
            _DetailSection(
              title: 'Overview',
              rows: [
                _DetailRow('Container ID', c.containerId),
                _DetailRow('Server', widget.serverName),
                _DetailRow('Image', c.image ?? '—'),
                if (c.imageId != null && c.imageId!.isNotEmpty)
                  _DetailRow('Image ID', c.imageId!),
                _DetailRow(
                  'Created',
                  c.createdAt == null ? '—' : c.createdAt!.toLocal().toString(),
                ),
                _DetailRow('Uptime', _uptime(c.createdAt)),
              ],
            ),
            const SizedBox(height: 16),
            _DetailSection(
              title: 'Ports',
              rows: c.ports.isEmpty
                  ? [_DetailRow('', 'No published ports')]
                  : c.ports
                        .map(
                          (p) => _DetailRow(
                            '${p.privatePort}/${p.type}',
                            p.publicPort == 0
                                ? 'not published'
                                : '${p.ip.isEmpty ? '0.0.0.0' : p.ip}:${p.publicPort}',
                          ),
                        )
                        .toList(),
            ),
            const SizedBox(height: 16),
            _DetailSection(
              title: 'Networks',
              rows: c.networks.isEmpty
                  ? [_DetailRow('', 'No networks')]
                  : c.networks
                        .map(
                          (n) => _DetailRow(
                            n.name,
                            n.ipAddress.isEmpty ? '—' : n.ipAddress,
                          ),
                        )
                        .toList(),
            ),
            const SizedBox(height: 16),
            _DetailSection(
              title: 'Volumes / Mounts',
              rows: c.mounts.isEmpty
                  ? [_DetailRow('', 'No mounts')]
                  : c.mounts
                        .map(
                          (m) => _DetailRow(
                            m.name.isEmpty ? m.source : m.name,
                            '${m.destination} (${m.readWrite ? 'rw' : 'ro'})',
                          ),
                        )
                        .toList(),
            ),
            const SizedBox(height: 16),
            Text('Details', style: Theme.of(context).textTheme.titleMedium),
            const SizedBox(height: 8),
            FutureBuilder<ContainerDetail>(
              future: _detailFuture,
              builder: (context, snapshot) {
                if (snapshot.connectionState == ConnectionState.waiting) {
                  return const Padding(
                    padding: EdgeInsets.symmetric(vertical: 16),
                    child: Center(child: CircularProgressIndicator()),
                  );
                }
                if (snapshot.hasError) {
                  return Padding(
                    padding: const EdgeInsets.symmetric(vertical: 8),
                    child: Text(
                      'Failed to load container details: ${snapshot.error}',
                      style: const TextStyle(color: Colors.orange),
                    ),
                  );
                }
                final detail = snapshot.data!;
                return Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Row(
                      mainAxisAlignment: MainAxisAlignment.spaceBetween,
                      children: [
                        Text(
                          'Restart policy',
                          style: Theme.of(context).textTheme.titleSmall,
                        ),
                        TextButton(
                          onPressed: _busy
                              ? null
                              : () => _restartPolicy(detail),
                          child: const Text('Change'),
                        ),
                      ],
                    ),
                    _DetailSection(
                      title: '',
                      rows: [
                        _DetailRow(
                          'Policy',
                          detail.restartPolicyName.isEmpty
                              ? '—'
                              : detail.restartPolicyName,
                        ),
                        if (detail.restartPolicyMaxRetryCount > 0)
                          _DetailRow(
                            'Max retries',
                            '${detail.restartPolicyMaxRetryCount}',
                          ),
                        _DetailRow('Restart count', '${detail.restartCount}'),
                      ],
                    ),
                    const SizedBox(height: 16),
                    _DetailSection(
                      title: 'Health',
                      rows: [
                        _DetailRow(
                          'Status',
                          detail.healthStatus.isEmpty
                              ? 'no healthcheck configured'
                              : detail.healthStatus,
                        ),
                        if (detail.healthFailingStreak > 0)
                          _DetailRow(
                            'Failing streak',
                            '${detail.healthFailingStreak}',
                          ),
                      ],
                    ),
                    const SizedBox(height: 16),
                    Row(
                      mainAxisAlignment: MainAxisAlignment.spaceBetween,
                      children: [
                        Text(
                          'Environment variables',
                          style: Theme.of(context).textTheme.titleSmall,
                        ),
                        Row(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            if (_rollbackHistory.isNotEmpty)
                              TextButton.icon(
                                onPressed: _busy ? null : _rollback,
                                icon: const Icon(Icons.history, size: 16),
                                label: const Text('Rollback'),
                              ),
                            TextButton.icon(
                              onPressed: _busy ? null : () => _recreate(detail),
                              icon: const Icon(Icons.refresh, size: 16),
                              label: const Text('Recreate'),
                            ),
                          ],
                        ),
                      ],
                    ),
                    _DetailSection(
                      title: '',
                      rows: detail.env.isEmpty
                          ? [_DetailRow('', 'No environment variables')]
                          : detail.env.map((e) {
                              final parts = e.split('=');
                              final key = parts.first;
                              final value = parts.length > 1
                                  ? parts.sublist(1).join('=')
                                  : '';
                              return _DetailRow(key, value);
                            }).toList(),
                    ),
                  ],
                );
              },
            ),
          ],
        ),
      ),
    );
  }
}

class _DetailRow {
  final String label;
  final String value;
  const _DetailRow(this.label, this.value);
}

class _DetailSection extends StatelessWidget {
  final String title;
  final List<_DetailRow> rows;

  const _DetailSection({required this.title, required this.rows});

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        if (title.isNotEmpty)
          Text(title, style: Theme.of(context).textTheme.titleSmall),
        if (title.isNotEmpty) const SizedBox(height: 4),
        for (final row in rows)
          Padding(
            padding: const EdgeInsets.symmetric(vertical: 2),
            child: row.label.isEmpty
                ? Text(row.value, style: Theme.of(context).textTheme.bodySmall)
                : Row(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      SizedBox(
                        width: 140,
                        child: Text(
                          row.label,
                          style: Theme.of(context).textTheme.bodySmall,
                        ),
                      ),
                      Expanded(
                        child: Text(
                          row.value,
                          style: Theme.of(context).textTheme.bodyMedium,
                        ),
                      ),
                    ],
                  ),
          ),
      ],
    );
  }
}
