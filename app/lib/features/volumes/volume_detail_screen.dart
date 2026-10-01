import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/image.dart' show formatBytes;
import '../../models/volume.dart';
import 'volume_delete_dialog.dart';
import 'volume_files_screen.dart';
import '../../theme/app_theme.dart';

/// Full metadata for one volume — driver, mountpoint, labels, size, and
/// which containers currently mount it — fetched on demand from
/// GET /api/servers/{id}/volumes/{name}.
class VolumeDetailScreen extends StatefulWidget {
  final ApiClient apiClient;
  final VolumeSummary volume;
  final bool isAdmin;

  const VolumeDetailScreen({
    super.key,
    required this.apiClient,
    required this.volume,
    this.isAdmin = false,
  });

  @override
  State<VolumeDetailScreen> createState() => _VolumeDetailScreenState();
}

class _VolumeDetailScreenState extends State<VolumeDetailScreen> {
  late Future<VolumeSummary> _detailFuture;

  @override
  void initState() {
    super.initState();
    _detailFuture = widget.apiClient.inspectVolume(
      widget.volume.serverId,
      widget.volume.name,
    );
  }

  Future<void> _delete(VolumeSummary volume) async {
    final deleted = await showVolumeDeleteDialog(
      context,
      apiClient: widget.apiClient,
      volume: volume,
    );
    if (deleted == true && mounted) Navigator.of(context).pop(true);
  }

  Future<void> _clone(VolumeSummary volume) async {
    var newName = '${volume.name}-copy';
    final target = await showDialog<String>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Clone volume'),
        content: TextFormField(
          initialValue: newName,
          onChanged: (value) => newName = value,
          decoration: const InputDecoration(labelText: 'New volume name'),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, newName.trim()),
            child: const Text('Clone'),
          ),
        ],
      ),
    );
    if (target == null || target.isEmpty || !mounted) return;
    try {
      await widget.apiClient.cloneVolume(volume.serverId, volume.name, target);
      if (mounted) Navigator.of(context).pop(true);
    } catch (error) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Clone failed: $error')));
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: Text(widget.volume.name)),
      body: FutureBuilder<VolumeSummary>(
        future: _detailFuture,
        builder: (context, snapshot) {
          final volume = snapshot.data ?? widget.volume;
          if (snapshot.connectionState == ConnectionState.waiting) {
            return const Center(child: CircularProgressIndicator());
          }
          if (snapshot.hasError) {
            return Center(
              child: Text('Failed to load volume: ${snapshot.error}'),
            );
          }

          return ListView(
            padding: const EdgeInsets.all(16),
            children: [
              if (volume.orphaned)
                Card(
                  color: AppColors.warning.withValues(alpha: 0.1),
                  child: const Padding(
                    padding: EdgeInsets.all(12),
                    child: Row(
                      children: [
                        Icon(Icons.warning_amber, color: AppColors.warning),
                        SizedBox(width: 12),
                        Expanded(
                          child: Text(
                            'This volume is orphaned — no container currently mounts it.',
                          ),
                        ),
                      ],
                    ),
                  ),
                ),
              const SizedBox(height: 12),
              _section('Metadata', [
                _row('Server', volume.serverName),
                _row('Driver', volume.driver),
                _row('Mountpoint', volume.mountpoint),
                _row('Size', formatBytes(volume.sizeBytes)),
                if (volume.createdAt != null)
                  _row(
                    'Created',
                    volume.createdAt!.toLocal().toString().split('.').first,
                  ),
              ]),
              const SizedBox(height: 16),
              _section(
                'In use by',
                volume.inUseBy.isEmpty
                    ? [const Text('No containers currently mount this volume.')]
                    : [
                        for (final containerId in volume.inUseBy)
                          Text(
                            containerId.length > 12
                                ? containerId.substring(0, 12)
                                : containerId,
                          ),
                      ],
              ),
              const SizedBox(height: 16),
              if (volume.labels.isNotEmpty)
                _section('Labels', [
                  for (final entry in volume.labels.entries)
                    _row(entry.key, entry.value),
                ]),
              const SizedBox(height: 16),
              Card(
                child: Padding(
                  padding: const EdgeInsets.all(12),
                  child: Row(
                    children: [
                      const Icon(Icons.info_outline, size: 18),
                      const SizedBox(width: 12),
                      const Expanded(
                        child: Text(
                          'Docker cannot attach or detach a volume on a running '
                          'container without recreating it. To attach or detach '
                          'this volume, edit the container\'s configuration '
                          '(Volumes) and recreate it — it will be briefly stopped.',
                        ),
                      ),
                    ],
                  ),
                ),
              ),
              const SizedBox(height: 24),
              if (widget.isAdmin)
                OutlinedButton.icon(
                  onPressed: () => Navigator.of(context).push(
                    MaterialPageRoute(
                      builder: (_) => VolumeFilesScreen(
                        apiClient: widget.apiClient,
                        volume: volume,
                        isAdmin: widget.isAdmin,
                      ),
                    ),
                  ),
                  icon: const Icon(Icons.folder_open_outlined),
                  label: const Text('Browse files'),
                ),
              if (widget.isAdmin && volume.orphaned) ...[
                const SizedBox(height: 8),
                OutlinedButton.icon(
                  onPressed: () => _clone(volume),
                  icon: const Icon(Icons.copy_all_outlined),
                  label: const Text('Clone volume'),
                ),
              ],
              const SizedBox(height: 8),
              if (widget.isAdmin)
                OutlinedButton.icon(
                  onPressed: () => _delete(volume),
                  icon: const Icon(
                    Icons.delete_outline,
                    color: AppColors.failed,
                  ),
                  label: const Text(
                    'Delete volume',
                    style: TextStyle(color: AppColors.failed),
                  ),
                ),
            ],
          );
        },
      ),
    );
  }

  Widget _section(String title, List<Widget> children) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(title, style: Theme.of(context).textTheme.titleMedium),
        const SizedBox(height: 8),
        ...children,
      ],
    );
  }

  Widget _row(String label, String value) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 2),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 100,
            child: Text(
              label,
              style: const TextStyle(fontWeight: FontWeight.w500),
            ),
          ),
          Expanded(child: Text(value.isEmpty ? '—' : value)),
        ],
      ),
    );
  }
}
