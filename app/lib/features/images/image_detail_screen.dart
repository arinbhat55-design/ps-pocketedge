import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/image.dart';

const _severityOrder = ['critical', 'high', 'medium', 'low', 'unknown'];
const _severityColors = {
  'critical': Colors.red,
  'high': Colors.deepOrange,
  'medium': Colors.orange,
  'low': Colors.blueGrey,
  'unknown': Colors.grey,
};

/// Full detail for one image: the cheap [ImageSummary] fields (already
/// known from the list, shown immediately) plus the expensive [ImageDetail]
/// fields (layers, env, labels, architecture), fetched on demand. Also
/// hosts vulnerability scanning and "is a newer image available" checks.
class ImageDetailScreen extends StatefulWidget {
  final ApiClient apiClient;
  final ImageSummary image;

  const ImageDetailScreen({
    super.key,
    required this.apiClient,
    required this.image,
  });

  @override
  State<ImageDetailScreen> createState() => _ImageDetailScreenState();
}

class _ImageDetailScreenState extends State<ImageDetailScreen> {
  late Future<ImageDetail> _detailFuture;
  Future<ScanResult?>? _scanFuture;
  ImageUpdateStatus? _updateStatus;
  bool _checkingUpdate = false;
  bool _scanning = false;
  String? _scanError;

  @override
  void initState() {
    super.initState();
    _detailFuture = widget.apiClient.inspectImage(
      widget.image.serverId,
      widget.image.id,
    );
    if (widget.image.repoTags.isNotEmpty) {
      _scanFuture = widget.apiClient.getImageScan(widget.image.repoTags.first);
    }
  }

  Future<void> _checkForUpdate() async {
    if (widget.image.repoTags.isEmpty) return;
    setState(() => _checkingUpdate = true);
    try {
      final status = await widget.apiClient.imageUpdateAvailable(
        serverId: widget.image.serverId,
        imageId: widget.image.id,
        ref: widget.image.repoTags.first,
      );
      if (mounted) setState(() => _updateStatus = status);
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Failed to check for update: $e')));
      }
    } finally {
      if (mounted) setState(() => _checkingUpdate = false);
    }
  }

  Future<void> _runScan() async {
    if (widget.image.repoTags.isEmpty) {
      setState(
        () => _scanError = 'This image has no tag to scan (dangling image).',
      );
      return;
    }
    setState(() {
      _scanning = true;
      _scanError = null;
    });
    try {
      final result = await widget.apiClient.scanImage(
        widget.image.repoTags.first,
      );
      if (mounted) {
        setState(() => _scanFuture = Future.value(result));
      }
    } catch (e) {
      if (mounted) setState(() => _scanError = '$e');
    } finally {
      if (mounted) setState(() => _scanning = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final img = widget.image;
    final title = img.repoTags.isNotEmpty ? img.repoTags.first : img.shortId;

    return Scaffold(
      appBar: AppBar(title: Text(title)),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          if (img.dangling)
            const Card(
              color: Colors.amber,
              child: Padding(
                padding: EdgeInsets.all(12),
                child: Row(
                  children: [
                    Icon(Icons.warning_amber, size: 18),
                    SizedBox(width: 8),
                    Text('This is a dangling image (no tags reference it).'),
                  ],
                ),
              ),
            ),
          if (img.dangling) const SizedBox(height: 16),
          _Section(
            title: 'Overview',
            rows: [
              _Row('Server', img.serverName),
              _Row('Id', img.shortId),
              if (img.repoTags.isNotEmpty)
                _Row('Tags', img.repoTags.join(', ')),
              _Row('Size', formatBytes(img.sizeBytes)),
              _Row('Created', img.createdAt.toLocal().toString()),
              _Row('Containers using this image', '${img.containersCount}'),
            ],
          ),
          const SizedBox(height: 16),
          Row(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              Text('Updates', style: Theme.of(context).textTheme.titleSmall),
              TextButton.icon(
                onPressed: img.repoTags.isEmpty || _checkingUpdate
                    ? null
                    : _checkForUpdate,
                icon: _checkingUpdate
                    ? const SizedBox(
                        width: 14,
                        height: 14,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : const Icon(Icons.system_update_alt, size: 16),
                label: const Text('Check for newer image'),
              ),
            ],
          ),
          if (_updateStatus != null)
            Card(
              child: Padding(
                padding: const EdgeInsets.all(12),
                child: Row(
                  children: [
                    Icon(
                      _updateStatus!.upToDate
                          ? Icons.check_circle
                          : Icons.new_releases,
                      color: _updateStatus!.upToDate
                          ? Colors.green
                          : Colors.orange,
                      size: 18,
                    ),
                    const SizedBox(width: 8),
                    Expanded(
                      child: Text(
                        _updateStatus!.upToDate
                            ? 'Up to date with the registry.'
                            : 'A newer image is available in the registry.',
                      ),
                    ),
                  ],
                ),
              ),
            ),
          const SizedBox(height: 16),
          Row(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              Text(
                'Vulnerability scan',
                style: Theme.of(context).textTheme.titleSmall,
              ),
              TextButton.icon(
                onPressed: _scanning ? null : _runScan,
                icon: _scanning
                    ? const SizedBox(
                        width: 14,
                        height: 14,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : const Icon(Icons.security, size: 16),
                label: Text(_scanning ? 'Scanning…' : 'Scan now'),
              ),
            ],
          ),
          if (_scanError != null)
            Padding(
              padding: const EdgeInsets.only(top: 4),
              child: Text(_scanError!, style: const TextStyle(color: Colors.red)),
            ),
          if (_scanFuture != null)
            FutureBuilder<ScanResult?>(
              future: _scanFuture,
              builder: (context, snapshot) {
                if (snapshot.connectionState == ConnectionState.waiting) {
                  return const Padding(
                    padding: EdgeInsets.symmetric(vertical: 8),
                    child: LinearProgressIndicator(),
                  );
                }
                final result = snapshot.data;
                if (result == null) {
                  return const Padding(
                    padding: EdgeInsets.symmetric(vertical: 8),
                    child: Text('No scan recorded yet.'),
                  );
                }
                return _ScanSummary(result: result);
              },
            ),
          const SizedBox(height: 16),
          FutureBuilder<ImageDetail>(
            future: _detailFuture,
            builder: (context, snapshot) {
              if (snapshot.connectionState == ConnectionState.waiting) {
                return const Center(
                  child: Padding(
                    padding: EdgeInsets.all(24),
                    child: CircularProgressIndicator(),
                  ),
                );
              }
              if (snapshot.hasError) {
                return Text('Failed to load image detail: ${snapshot.error}');
              }
              final detail = snapshot.data!;
              return Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  _Section(
                    title: 'Platform',
                    rows: [
                      _Row('Architecture', detail.architecture),
                      _Row('OS', detail.os),
                      _Row('Layers', '${detail.layers.length}'),
                    ],
                  ),
                  const SizedBox(height: 16),
                  Text('Layers', style: Theme.of(context).textTheme.titleSmall),
                  _Section(
                    title: '',
                    rows: detail.layers.isEmpty
                        ? [const _Row('', 'No layer information')]
                        : [
                            for (var i = 0; i < detail.layers.length; i++)
                              _Row(
                                'Layer ${i + 1}',
                                detail.layers[i].digest,
                              ),
                          ],
                  ),
                  const SizedBox(height: 16),
                  if (detail.labels.isNotEmpty) ...[
                    Text('Labels', style: Theme.of(context).textTheme.titleSmall),
                    _Section(
                      title: '',
                      rows: detail.labels.entries
                          .map((e) => _Row(e.key, e.value))
                          .toList(),
                    ),
                    const SizedBox(height: 16),
                  ],
                  Text(
                    'Environment variables',
                    style: Theme.of(context).textTheme.titleSmall,
                  ),
                  _Section(
                    title: '',
                    rows: detail.env.isEmpty
                        ? [const _Row('', 'No environment variables')]
                        : detail.env.map((e) {
                            final parts = e.split('=');
                            final key = parts.first;
                            final value = parts.length > 1
                                ? parts.sublist(1).join('=')
                                : '';
                            return _Row(key, value);
                          }).toList(),
                  ),
                ],
              );
            },
          ),
        ],
      ),
    );
  }
}

class _ScanSummary extends StatelessWidget {
  final ScanResult result;

  const _ScanSummary({required this.result});

  @override
  Widget build(BuildContext context) {
    final counts = {
      'critical': result.criticalCount,
      'high': result.highCount,
      'medium': result.mediumCount,
      'low': result.lowCount,
      'unknown': result.unknownCount,
    };

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            for (final severity in _severityOrder)
              if (counts[severity]! > 0)
                Chip(
                  label: Text('${counts[severity]} $severity'),
                  backgroundColor: _severityColors[severity]!.withValues(
                    alpha: 0.15,
                  ),
                  labelStyle: TextStyle(color: _severityColors[severity]),
                ),
            if (result.totalCount == 0)
              const Chip(label: Text('No known vulnerabilities')),
          ],
        ),
        if (result.vulnerabilities.isNotEmpty) ...[
          const SizedBox(height: 8),
          ...result.vulnerabilities
              .take(20)
              .map(
                (v) => ListTile(
                  dense: true,
                  contentPadding: EdgeInsets.zero,
                  leading: Icon(
                    Icons.circle,
                    size: 10,
                    color: _severityColors[v.severity.toLowerCase()] ??
                        Colors.grey,
                  ),
                  title: Text('${v.id} — ${v.pkgName}'),
                  subtitle: Text(
                    v.fixedVersion.isEmpty
                        ? v.title
                        : '${v.title.isEmpty ? '' : '${v.title} — '}fixed in ${v.fixedVersion}',
                  ),
                ),
              ),
        ],
      ],
    );
  }
}

class _Row {
  final String label;
  final String value;
  const _Row(this.label, this.value);
}

class _Section extends StatelessWidget {
  final String title;
  final List<_Row> rows;

  const _Section({required this.title, required this.rows});

  @override
  Widget build(BuildContext context) {
    return Card(
      margin: EdgeInsets.zero,
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            if (title.isNotEmpty) ...[
              Text(title, style: Theme.of(context).textTheme.titleSmall),
              const SizedBox(height: 8),
            ],
            for (final row in rows)
              Padding(
                padding: const EdgeInsets.symmetric(vertical: 2),
                child: Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    if (row.label.isNotEmpty)
                      SizedBox(
                        width: 160,
                        child: Text(
                          row.label,
                          style: const TextStyle(fontWeight: FontWeight.w600),
                        ),
                      ),
                    Expanded(
                      child: Text(
                        row.value,
                        style: const TextStyle(fontFamily: 'monospace'),
                      ),
                    ),
                  ],
                ),
              ),
          ],
        ),
      ),
    );
  }
}
