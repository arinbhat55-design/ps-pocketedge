import 'dart:async';

import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/image.dart' show formatBytes;
import '../../models/server.dart';
import '../../theme/app_theme.dart';
import '../../widgets/state_message.dart';
import '../../widgets/status_pill.dart';

/// Host readings for the selected agent, kept visible across app modules.
class RuntimeStatusBar extends StatefulWidget {
  final ApiClient apiClient;
  final bool isAdmin;
  final ValueChanged<Server> onOpenTerminal;

  const RuntimeStatusBar({
    super.key,
    required this.apiClient,
    required this.isAdmin,
    required this.onOpenTerminal,
  });

  @override
  State<RuntimeStatusBar> createState() => _RuntimeStatusBarState();
}

class _RuntimeStatusBarState extends State<RuntimeStatusBar> {
  List<Server> _servers = [];
  String? _selectedId;
  String? _error;
  bool _loading = true;
  bool _refreshing = false;
  Timer? _poll;

  Server? get _selected {
    for (final server in _servers) {
      if (server.id == _selectedId) return server;
    }
    return null;
  }

  @override
  void initState() {
    super.initState();
    _refresh();
    _poll = Timer.periodic(kListPollInterval, (_) => _refresh());
  }

  @override
  void dispose() {
    _poll?.cancel();
    super.dispose();
  }

  Future<void> _refresh() async {
    if (_refreshing) return;
    _refreshing = true;
    try {
      final servers = await widget.apiClient.listServers();
      if (!mounted) return;
      setState(() {
        _servers = servers;
        if (!servers.any((s) => s.id == _selectedId)) {
          _selectedId = servers.isEmpty
              ? null
              : (servers.where(isServerOnline).firstOrNull ?? servers.first).id;
        }
        _loading = false;
        _error = null;
      });
    } catch (_) {
      if (mounted) {
        setState(() {
          _loading = false;
          _error = 'Metrics unavailable';
        });
      }
    } finally {
      _refreshing = false;
    }
  }

  @override
  Widget build(BuildContext context) {
    final server = _selected;
    final online = server != null && isServerOnline(server) && _error == null;
    final resources = server?.lastResources;
    final status =
        _error ??
        (_loading
            ? 'Loading metrics…'
            : server == null
            ? 'No servers connected'
            : serverStatus(server).label);
    final terminalReason = !widget.isAdmin
        ? 'Administrator access required for terminals'
        : !online
        ? 'Connect a server to open its host terminal'
        : 'Open the host terminal';
    final terminal = Tooltip(
      message: terminalReason,
      child: TextButton.icon(
        key: const ValueKey('status-bar-terminal'),
        onPressed: widget.isAdmin && online
            ? () => widget.onOpenTerminal(server)
            : null,
        style: TextButton.styleFrom(
          minimumSize: const Size(0, 32),
          padding: const EdgeInsets.symmetric(horizontal: 10),
          tapTargetSize: MaterialTapTargetSize.shrinkWrap,
          textStyle: const TextStyle(fontSize: 11),
        ),
        icon: const Icon(Icons.terminal, size: 14),
        label: const Text('Terminal'),
      ),
    );
    final compact = MediaQuery.sizeOf(context).width < 600;
    final selector = PopupMenuButton<String>(
      tooltip: 'Select metrics server',
      enabled: _servers.isNotEmpty,
      initialValue: _selectedId,
      onSelected: (id) => setState(() => _selectedId = id),
      itemBuilder: (_) => [
        for (final s in _servers)
          PopupMenuItem(
            value: s.id,
            child: Text('${s.name} · ${serverStatus(s).label}'),
          ),
      ],
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 8),
        child: Row(
          children: [
            Icon(
              Icons.circle,
              size: 8,
              color: online
                  ? StatusTone.healthy.color
                  : StatusTone.neutral.color,
            ),
            SizedBox(width: compact ? 4 : 6),
            if (!compact)
              Expanded(
                child: Text(
                  server == null ? status : '${server.name} · $status',
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: const TextStyle(fontSize: 11),
                ),
              ),
            const Icon(Icons.expand_more, size: 14),
          ],
        ),
      ),
    );
    final metrics = SingleChildScrollView(
      scrollDirection: Axis.horizontal,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 10),
        child: Row(
          children: [
            _ResourceMetric(
              label: 'CPU',
              icon: Icons.speed,
              value: resources == null
                  ? '—'
                  : '${_percent(resources.cpuPercent)}%',
              percent: resources?.cpuPercent,
              tooltip: 'Total host CPU usage',
            ),
            const SizedBox(width: 20),
            _ResourceMetric(
              label: 'RAM',
              icon: Icons.memory,
              value: _usage(
                resources?.usedMemoryBytes,
                resources?.totalMemoryBytes,
                resources?.memPercent,
              ),
              percent: resources?.memPercent,
              tooltip: 'Total host RAM used / installed capacity',
            ),
            const SizedBox(width: 20),
            _ResourceMetric(
              label: 'Disk',
              icon: Icons.storage_outlined,
              value:
                  '${_usage(resources?.usedDiskBytes, resources?.totalDiskBytes, resources?.diskPercent)} limit',
              percent: resources?.diskPercent,
              tooltip:
                  'Host root filesystem: space used / total capacity (disk limit)',
            ),
            if (server != null && !online) ...[
              const SizedBox(width: 16),
              const Text('Last reported', style: TextStyle(fontSize: 11)),
            ],
          ],
        ),
      ),
    );
    return Material(
      key: const ValueKey('runtime-status-bar'),
      color: Theme.of(context).brightness == Brightness.dark
          ? AppColors.bottomBar
          : Theme.of(context).colorScheme.surfaceContainerLow,
      child: DecoratedBox(
        decoration: BoxDecoration(
          border: Border(
            top: BorderSide(
              color: Theme.of(context).brightness == Brightness.dark
                  ? AppColors.bottomBarBorder
                  : Theme.of(context).dividerColor,
              width: 1,
            ),
          ),
        ),
        child: SafeArea(
          top: false,
          child: SizedBox(
            height: 32,
            child: Row(
              children: [
                SizedBox(width: compact ? 44 : 180, child: selector),
                Expanded(child: metrics),
                if (_error != null)
                  IconButton(
                    tooltip: 'Retry metrics',
                    onPressed: _refresh,
                    padding: EdgeInsets.zero,
                    constraints: const BoxConstraints.tightFor(
                      width: 28,
                      height: 32,
                    ),
                    style: IconButton.styleFrom(
                      tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                    ),
                    icon: const Icon(Icons.refresh, size: 14),
                  ),
                terminal,
                const SizedBox(width: 4),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

String _percent(double value) =>
    value.isFinite ? value.clamp(0, 100).toStringAsFixed(1) : '—';

String _usage(int? used, int? total, double? percent) {
  final usedLabel = used == null
      ? percent == null
            ? '—'
            : '${_percent(percent)}%'
      : formatBytes(used);
  return '$usedLabel / ${total == null || total <= 0 ? '—' : formatBytes(total)}';
}

class _ResourceMetric extends StatelessWidget {
  final String label;
  final IconData icon;
  final String value;
  final double? percent;
  final String tooltip;

  const _ResourceMetric({
    required this.label,
    required this.icon,
    required this.value,
    required this.percent,
    required this.tooltip,
  });

  @override
  Widget build(BuildContext context) => Tooltip(
    message: tooltip,
    child: Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(
          icon,
          size: 14,
          color: percent != null && percent!.isFinite && percent! >= 75
              ? usageTone(percent!).color
              : Theme.of(context).colorScheme.onSurfaceVariant,
        ),
        const SizedBox(width: 6),
        Text('$label $value', style: const TextStyle(fontSize: 11)),
      ],
    ),
  );
}
