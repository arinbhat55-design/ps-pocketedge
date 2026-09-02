import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

import '../../api/api_client.dart';
import '../../models/log_line.dart';

/// Live (or bounded, once a date/time range is applied) log tail for one
/// container, with search/filter, highlighted warnings/errors, download,
/// combining sibling containers from the same deployment into one feed,
/// and an AI-assisted summary/root-cause panel.
class ContainerLogsScreen extends StatefulWidget {
  final ApiClient apiClient;
  final String serverId;
  final String containerId;
  final String containerName;
  final String? deploymentId;

  const ContainerLogsScreen({
    super.key,
    required this.apiClient,
    required this.serverId,
    required this.containerId,
    required this.containerName,
    this.deploymentId,
  });

  @override
  State<ContainerLogsScreen> createState() => _ContainerLogsScreenState();
}

/// Ring-buffer cap on retained lines — a runaway container under `follow`
/// shouldn't grow this screen's memory unbounded.
const _maxRetainedLines = 5000;

class _ContainerLogsScreenState extends State<ContainerLogsScreen> {
  WebSocketChannel? _channel;
  StreamSubscription<dynamic>? _sub;
  final List<LogLine> _lines = [];
  final _scrollController = ScrollController();
  final _searchController = TextEditingController();

  String _search = '';
  bool _useRegex = false;
  String? _streamError;
  bool _multiContainer = false;
  Set<String> _combinedWith = {};
  DateTime? _since;
  DateTime? _until;

  bool? _aiAvailable;
  bool _analyzing = false;
  LogAnalysis? _analysis;

  @override
  void initState() {
    super.initState();
    _connect();
    widget.apiClient.aiConfigured().then((ok) {
      if (mounted) setState(() => _aiAvailable = ok);
    });
  }

  void _connect() {
    _sub?.cancel();
    _channel?.sink.close();
    setState(() {
      _lines.clear();
      _streamError = null;
    });

    final uri = widget.apiClient.containerLogsStreamUri(
      widget.serverId,
      widget.containerId,
      since: _since,
      until: _until,
      withContainers: _combinedWith.toList(),
    );
    final channel = WebSocketChannel.connect(uri);
    _channel = channel;
    _multiContainer = _combinedWith.isNotEmpty;
    _sub = channel.stream.listen(
      (data) {
        final chunk = LogChunk.fromJson(
          jsonDecode(data as String) as Map<String, dynamic>,
        );
        if (chunk.error != null && chunk.error!.isNotEmpty) {
          setState(() => _streamError = chunk.error);
          return;
        }
        if (chunk.lines.isEmpty) return;
        setState(() {
          _lines.addAll(chunk.lines);
          if (_lines.length > _maxRetainedLines) {
            _lines.removeRange(0, _lines.length - _maxRetainedLines);
          }
        });
        _scrollToBottom();
      },
      onError: (Object e) => setState(() => _streamError = 'Disconnected: $e'),
      onDone: () {
        if (mounted) setState(() => _streamError ??= 'Log stream ended');
      },
    );
  }

  void _scrollToBottom() {
    if (!_scrollController.hasClients) return;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!_scrollController.hasClients) return;
      _scrollController.animateTo(
        _scrollController.position.maxScrollExtent,
        duration: const Duration(milliseconds: 150),
        curve: Curves.easeOut,
      );
    });
  }

  Iterable<LogLine> get _filtered {
    if (_search.isEmpty) return _lines;
    if (_useRegex) {
      try {
        final re = RegExp(_search, caseSensitive: false);
        return _lines.where((l) => re.hasMatch(l.message));
      } catch (_) {
        return _lines;
      }
    }
    final needle = _search.toLowerCase();
    return _lines.where((l) => l.message.toLowerCase().contains(needle));
  }

  Color? _severityColor(BuildContext context, String message) {
    final lower = message.toLowerCase();
    if (lower.contains('error') ||
        lower.contains('fatal') ||
        lower.contains('exception') ||
        lower.contains('panic')) {
      return Colors.redAccent;
    }
    if (lower.contains('warn')) return Colors.amber.shade700;
    return null;
  }

  Future<void> _pickRange() async {
    final now = DateTime.now();
    final since = await showDatePicker(
      context: context,
      initialDate: _since ?? now.subtract(const Duration(hours: 1)),
      firstDate: now.subtract(const Duration(days: 90)),
      lastDate: now,
      helpText: 'Show logs since',
    );
    if (since == null) return;
    if (!mounted) return;
    final sinceTime = await showTimePicker(
      context: context,
      initialTime: TimeOfDay.fromDateTime(_since ?? now),
    );
    final combinedSince = DateTime(
      since.year,
      since.month,
      since.day,
      sinceTime?.hour ?? 0,
      sinceTime?.minute ?? 0,
    );

    if (!mounted) return;
    final untilConfirm = await showDialog<bool>(
      context: context,
      builder: (_) => AlertDialog(
        title: const Text('Set an end time too?'),
        content: const Text(
          'Optional — leave unset to keep following logs live from this point.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(false),
            child: const Text('No end time'),
          ),
          FilledButton(
            onPressed: () => Navigator.of(context).pop(true),
            child: const Text('Set end time'),
          ),
        ],
      ),
    );

    DateTime? combinedUntil;
    if (untilConfirm == true && mounted) {
      final until = await showDatePicker(
        context: context,
        initialDate: _until ?? now,
        firstDate: combinedSince,
        lastDate: now,
        helpText: 'Show logs until',
      );
      if (until != null && mounted) {
        final untilTime = await showTimePicker(
          context: context,
          initialTime: TimeOfDay.fromDateTime(_until ?? now),
        );
        combinedUntil = DateTime(
          until.year,
          until.month,
          until.day,
          untilTime?.hour ?? 23,
          untilTime?.minute ?? 59,
        );
      }
    }

    setState(() {
      _since = combinedSince;
      _until = combinedUntil;
    });
    _connect();
  }

  void _clearRange() {
    setState(() {
      _since = null;
      _until = null;
    });
    _connect();
  }

  Future<void> _pickCombine() async {
    List<Map<String, String>> siblings = [];
    try {
      final containers = await widget.apiClient.listContainers(
        serverId: widget.serverId,
      );
      siblings = containers
          .where(
            (c) =>
                c.container.containerId != widget.containerId &&
                widget.deploymentId != null &&
                c.container.deploymentId == widget.deploymentId,
          )
          .map((c) => {'id': c.container.containerId, 'name': c.container.name})
          .toList();
    } catch (_) {
      // Fall through with an empty list — dialog just shows "none found".
    }

    if (!mounted) return;
    if (siblings.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text('No other containers found in this deployment'),
        ),
      );
      return;
    }

    final selected = Set<String>.from(_combinedWith);
    final result = await showDialog<Set<String>>(
      context: context,
      builder: (context) => StatefulBuilder(
        builder: (context, setDialogState) => AlertDialog(
          title: const Text('Combine logs'),
          content: SizedBox(
            width: 360,
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                for (final s in siblings)
                  CheckboxListTile(
                    value: selected.contains(s['id']),
                    title: Text(s['name'] ?? s['id']!),
                    onChanged: (checked) => setDialogState(() {
                      if (checked == true) {
                        selected.add(s['id']!);
                      } else {
                        selected.remove(s['id']);
                      }
                    }),
                  ),
              ],
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(context).pop(null),
              child: const Text('Cancel'),
            ),
            FilledButton(
              onPressed: () => Navigator.of(context).pop(selected),
              child: const Text('Apply'),
            ),
          ],
        ),
      ),
    );
    if (result == null) return;
    setState(() => _combinedWith = result);
    _connect();
  }

  Future<void> _download() async {
    try {
      final text = await widget.apiClient.downloadContainerLogs(
        widget.serverId,
        widget.containerId,
        since: _since,
        until: _until,
      );
      if (!mounted) return;
      await showDialog<void>(
        context: context,
        builder: (context) => AlertDialog(
          title: Text('${widget.containerName} — logs'),
          content: SizedBox(
            width: 600,
            height: 500,
            child: SingleChildScrollView(
              child: SelectableText(
                text,
                style: const TextStyle(fontFamily: 'monospace', fontSize: 12),
              ),
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(context).pop(),
              child: const Text('Close'),
            ),
          ],
        ),
      );
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Failed to fetch logs: $e')));
      }
    }
  }

  Future<void> _analyze() async {
    setState(() {
      _analyzing = true;
      _analysis = null;
    });
    try {
      final analysis = await widget.apiClient.analyzeContainerLogs(
        widget.serverId,
        widget.containerId,
      );
      if (mounted) setState(() => _analysis = analysis);
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('AI analysis failed: $e')));
      }
    } finally {
      if (mounted) setState(() => _analyzing = false);
    }
  }

  @override
  void dispose() {
    _sub?.cancel();
    _channel?.sink.close();
    _scrollController.dispose();
    _searchController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        Padding(
          padding: const EdgeInsets.all(8),
          child: Row(
            children: [
              Expanded(
                child: TextField(
                  controller: _searchController,
                  decoration: InputDecoration(
                    isDense: true,
                    prefixIcon: const Icon(Icons.search, size: 18),
                    hintText: _useRegex ? 'Search (regex)' : 'Search logs',
                    border: const OutlineInputBorder(),
                    suffixIcon: _search.isEmpty
                        ? null
                        : IconButton(
                            icon: const Icon(Icons.clear, size: 16),
                            onPressed: () {
                              _searchController.clear();
                              setState(() => _search = '');
                            },
                          ),
                  ),
                  onChanged: (v) => setState(() => _search = v),
                ),
              ),
              const SizedBox(width: 4),
              IconButton(
                tooltip: _useRegex ? 'Regex search on' : 'Plain text search',
                icon: Icon(
                  Icons.code,
                  color: _useRegex
                      ? Theme.of(context).colorScheme.primary
                      : null,
                ),
                onPressed: () => setState(() => _useRegex = !_useRegex),
              ),
              IconButton(
                tooltip: _since == null
                    ? 'Set date/time range'
                    : 'Date/time range set',
                icon: Icon(
                  Icons.date_range,
                  color: _since != null
                      ? Theme.of(context).colorScheme.primary
                      : null,
                ),
                onPressed: _pickRange,
              ),
              if (_since != null)
                IconButton(
                  tooltip: 'Clear range',
                  icon: const Icon(Icons.close, size: 16),
                  onPressed: _clearRange,
                ),
              IconButton(
                tooltip: 'Combine logs from related containers',
                icon: Icon(
                  Icons.merge,
                  color: _combinedWith.isNotEmpty
                      ? Theme.of(context).colorScheme.primary
                      : null,
                ),
                onPressed: _pickCombine,
              ),
              IconButton(
                tooltip: 'Download logs',
                icon: const Icon(Icons.download),
                onPressed: _download,
              ),
              if (_aiAvailable == true)
                IconButton(
                  tooltip: 'AI-assisted summary & root cause',
                  icon: _analyzing
                      ? const SizedBox(
                          width: 18,
                          height: 18,
                          child: CircularProgressIndicator(strokeWidth: 2),
                        )
                      : const Icon(Icons.auto_awesome),
                  onPressed: _analyzing ? null : _analyze,
                ),
            ],
          ),
        ),
        if (_streamError != null)
          Container(
            width: double.infinity,
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
            color: Colors.orange.withValues(alpha: 0.15),
            child: Text(_streamError!, style: const TextStyle(fontSize: 12)),
          ),
        if (_analysis != null) _AnalysisPanel(analysis: _analysis!),
        Expanded(
          child: Container(
            color: Theme.of(context).brightness == Brightness.dark
                ? Colors.black
                : const Color(0xFFF5F5F5),
            child: ListView.builder(
              controller: _scrollController,
              padding: const EdgeInsets.all(8),
              itemCount: _filtered.length,
              itemBuilder: (context, index) {
                final line = _filtered.elementAt(index);
                final color = _severityColor(context, line.message);
                final ts = line.timestamp;
                final prefix = _multiContainer
                    ? '${line.containerId.substring(0, line.containerId.length < 8 ? line.containerId.length : 8)} '
                    : '';
                return SelectableText.rich(
                  TextSpan(
                    style: const TextStyle(
                      fontFamily: 'monospace',
                      fontSize: 12,
                    ),
                    children: [
                      if (ts != null)
                        TextSpan(
                          text:
                              '${ts.toLocal().toIso8601String().substring(11, 19)} ',
                          style: const TextStyle(color: Colors.grey),
                        ),
                      if (prefix.isNotEmpty)
                        TextSpan(
                          text: prefix,
                          style: const TextStyle(color: Colors.blueGrey),
                        ),
                      TextSpan(
                        text: line.message,
                        style: color == null ? null : TextStyle(color: color),
                      ),
                    ],
                  ),
                );
              },
            ),
          ),
        ),
      ],
    );
  }
}

class _AnalysisPanel extends StatelessWidget {
  final LogAnalysis analysis;
  const _AnalysisPanel({required this.analysis});

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      margin: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: Theme.of(context).colorScheme.surfaceContainerHighest,
        borderRadius: BorderRadius.circular(8),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              const Icon(Icons.auto_awesome, size: 16),
              const SizedBox(width: 6),
              Text('AI summary', style: Theme.of(context).textTheme.titleSmall),
            ],
          ),
          const SizedBox(height: 6),
          Text(analysis.summary),
          if (analysis.rootCause.isNotEmpty) ...[
            const SizedBox(height: 8),
            Text(
              'Likely root cause',
              style: Theme.of(context).textTheme.labelMedium,
            ),
            Text(analysis.rootCause),
          ],
          if (analysis.recommendation.isNotEmpty) ...[
            const SizedBox(height: 8),
            Text(
              'Recommendation',
              style: Theme.of(context).textTheme.labelMedium,
            ),
            Text(analysis.recommendation),
          ],
        ],
      ),
    );
  }
}
