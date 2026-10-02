import 'dart:async';

import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/backup.dart';
import '../../theme/app_theme.dart';

/// Lists a deployment's backups and lets the user take a new one or
/// restore an existing one. Restore progress itself shows up on the
/// deployment's normal live status screen (not here) — see this screen's
/// pop value: true means "a restore was triggered, go reconnect."
class BackupsScreen extends StatefulWidget {
  final ApiClient apiClient;
  final String deploymentId;

  const BackupsScreen({
    super.key,
    required this.apiClient,
    required this.deploymentId,
  });

  @override
  State<BackupsScreen> createState() => _BackupsScreenState();
}

class _BackupsScreenState extends State<BackupsScreen> {
  List<Backup> _backups = [];
  bool _loading = true;
  String? _error;
  bool _creating = false;
  Timer? _pollTimer;

  @override
  void initState() {
    super.initState();
    _refresh();
  }

  @override
  void dispose() {
    _pollTimer?.cancel();
    super.dispose();
  }

  Future<void> _refresh() async {
    try {
      final backups = await widget.apiClient.listBackups(widget.deploymentId);
      if (!mounted) return;
      setState(() {
        _backups = backups;
        _loading = false;
        _error = null;
      });
      _syncPolling();
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = 'Failed to load backups: $e';
        _loading = false;
      });
    }
  }

  // Backup progress (agent snapshotting + uploading) isn't pushed live
  // like deployment status is — there's no WebSocket for it, just this
  // list endpoint — so while anything is still pending/running, poll it
  // every few seconds. Stops itself once nothing is in flight.
  void _syncPolling() {
    final anyInFlight = _backups.any((b) => !b.isTerminal);
    if (anyInFlight && _pollTimer == null) {
      _pollTimer = Timer.periodic(
        const Duration(seconds: 3),
        (_) => _refresh(),
      );
    } else if (!anyInFlight && _pollTimer != null) {
      _pollTimer?.cancel();
      _pollTimer = null;
    }
  }

  Future<void> _createBackup() async {
    setState(() => _creating = true);
    try {
      await widget.apiClient.createBackup(widget.deploymentId);
      await _refresh();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Failed to start backup: $e')));
      }
    } finally {
      if (mounted) setState(() => _creating = false);
    }
  }

  Future<void> _restore(Backup backup) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (_) => AlertDialog(
        title: const Text('Restore this backup?'),
        content: Text(
          'This overwrites the current state of this deployment with the '
          'snapshot from ${backup.createdAt.toLocal()}. The deployment will '
          'be briefly unavailable while it redeploys.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.of(context).pop(true),
            child: const Text('Restore'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;

    try {
      await widget.apiClient.restoreBackup(backup.id);
      if (mounted) Navigator.of(context).pop(true);
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Failed to restore: $e')));
      }
    }
  }

  Color _statusColor(String status) {
    switch (status) {
      case 'completed':
        return AppColors.healthy;
      case 'failed':
        return AppColors.failed;
      default:
        return AppColors.warning;
    }
  }

  String _formatSize(int? bytes) {
    if (bytes == null) return '';
    if (bytes < 1024 * 1024) return '${(bytes / 1024).toStringAsFixed(0)} KB';
    return '${(bytes / (1024 * 1024)).toStringAsFixed(1)} MB';
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Backups')),
      floatingActionButton: FloatingActionButton.extended(
        onPressed: _creating ? null : _createBackup,
        icon: _creating
            ? const SizedBox(
                width: 18,
                height: 18,
                child: CircularProgressIndicator(strokeWidth: 2),
              )
            : const Icon(Icons.add),
        label: const Text('Backup now'),
      ),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : _error != null
          ? Center(child: Text(_error!))
          : RefreshIndicator(
              onRefresh: _refresh,
              child: _backups.isEmpty
                  ? ListView(
                      children: const [
                        Padding(
                          padding: EdgeInsets.all(24),
                          child: Text('No backups yet.'),
                        ),
                      ],
                    )
                  : ListView.builder(
                      itemCount: _backups.length,
                      itemBuilder: (context, index) {
                        final b = _backups[index];
                        return ListTile(
                          leading: Icon(
                            Icons.circle,
                            size: 12,
                            color: _statusColor(b.status),
                          ),
                          title: Text(b.createdAt.toLocal().toString()),
                          subtitle: Text(
                            b.status == 'failed' && b.message.isNotEmpty
                                ? '${b.status} — ${b.message}'
                                : '${b.status}${b.sizeBytes != null ? ' • ${_formatSize(b.sizeBytes)}' : ''}',
                          ),
                          trailing: b.status == 'completed'
                              ? OutlinedButton(
                                  onPressed: () => _restore(b),
                                  child: const Text('Restore'),
                                )
                              : (!b.isTerminal
                                    ? const SizedBox(
                                        width: 18,
                                        height: 18,
                                        child: CircularProgressIndicator(
                                          strokeWidth: 2,
                                        ),
                                      )
                                    : null),
                        );
                      },
                    ),
            ),
    );
  }
}
