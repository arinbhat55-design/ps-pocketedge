import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../api/api_client.dart';
import '../../models/compose_file.dart';

/// A comparable snapshot: either a past version (fetched lazily) or the
/// compose file's current live content, so "Current" can sit alongside
/// past versions in the same selectable list for "Compare two versions".
class _Snapshot {
  final String id; // version id, or 'current'
  final String label;
  final String? content; // null for a past version not yet fetched
  const _Snapshot({required this.id, required this.label, this.content});
}

/// "Maintain Compose file version history" + "Compare two versions":
/// lists every past snapshot of a compose file (captured automatically by
/// UpdateComposeFile), lets the user restore one, or pick exactly two
/// (including "Current") to view as a line diff.
class ComposeVersionHistoryScreen extends StatefulWidget {
  final ApiClient apiClient;
  final ComposeFile composeFile;

  const ComposeVersionHistoryScreen({
    super.key,
    required this.apiClient,
    required this.composeFile,
  });

  @override
  State<ComposeVersionHistoryScreen> createState() =>
      _ComposeVersionHistoryScreenState();
}

class _ComposeVersionHistoryScreenState
    extends State<ComposeVersionHistoryScreen> {
  late Future<List<ComposeFileVersionSummary>> _versionsFuture;
  final Set<String> _selected = {};
  bool _restoring = false;
  bool _changed = false;

  @override
  void initState() {
    super.initState();
    _versionsFuture = widget.apiClient.listComposeFileVersions(
      widget.composeFile.id,
    );
  }

  void _toggleSelected(String id) {
    setState(() {
      if (_selected.contains(id)) {
        _selected.remove(id);
      } else {
        if (_selected.length >= 2) {
          _selected.remove(_selected.first);
        }
        _selected.add(id);
      }
    });
  }

  Future<String> _contentFor(String id) async {
    if (id == 'current') return widget.composeFile.content;
    final version = await widget.apiClient.getComposeFileVersion(
      widget.composeFile.id,
      id,
    );
    return version.content;
  }

  Future<void> _compare(List<_Snapshot> snapshots) async {
    if (_selected.length != 2) return;
    final ids = _selected.toList();
    // Order chronologically (older first) using the summaries' position:
    // 'current' is always the newest.
    final a = snapshots.firstWhere((s) => s.id == ids[0]);
    final b = snapshots.firstWhere((s) => s.id == ids[1]);
    final older = a.id == 'current' ? b : (b.id == 'current' ? a : (_isOlder(a, b, snapshots) ? a : b));
    final newer = older.id == a.id ? b : a;

    try {
      final oldContent = older.content ?? await _contentFor(older.id);
      final newContent = newer.content ?? await _contentFor(newer.id);
      if (!mounted) return;
      await Navigator.of(context).push(
        MaterialPageRoute(
          builder: (_) => ComposeDiffScreen(
            oldLabel: older.label,
            oldContent: oldContent,
            newLabel: newer.label,
            newContent: newContent,
          ),
        ),
      );
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Failed to load versions: $e')));
      }
    }
  }

  bool _isOlder(_Snapshot a, _Snapshot b, List<_Snapshot> ordered) {
    return ordered.indexOf(a) > ordered.indexOf(b);
  }

  Future<void> _restore(ComposeFileVersionSummary v) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (_) => AlertDialog(
        title: Text('Restore version ${v.versionNumber}?'),
        content: const Text(
          'This replaces the current content with this version. The '
          'current content is kept in history and can be restored again.',
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

    setState(() => _restoring = true);
    try {
      await widget.apiClient.restoreComposeFileVersion(
        widget.composeFile.id,
        v.id,
      );
      if (mounted) {
        setState(() {
          _changed = true;
          _versionsFuture = widget.apiClient.listComposeFileVersions(
            widget.composeFile.id,
          );
          _selected.clear();
        });
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Restored version ${v.versionNumber}.')),
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Failed to restore: $e')));
      }
    } finally {
      if (mounted) setState(() => _restoring = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text('${widget.composeFile.name} — history'),
        leading: BackButton(
          onPressed: () => Navigator.of(context).pop(_changed),
        ),
      ),
      body: FutureBuilder<List<ComposeFileVersionSummary>>(
          future: _versionsFuture,
          builder: (context, snapshot) {
            if (snapshot.connectionState == ConnectionState.waiting) {
              return const Center(child: CircularProgressIndicator());
            }
            if (snapshot.hasError) {
              return Center(child: Text('Failed to load history: ${snapshot.error}'));
            }
            final versions = snapshot.data ?? [];
            final snapshots = <_Snapshot>[
              _Snapshot(
                id: 'current',
                label: 'Current (v${widget.composeFile.version})',
                content: widget.composeFile.content,
              ),
              for (final v in versions)
                _Snapshot(id: v.id, label: 'v${v.versionNumber} — ${v.name}'),
            ];

            return Column(
              children: [
                if (versions.isEmpty)
                  const Expanded(
                    child: Center(
                      child: Text(
                        'No past versions yet.\nEditing and saving this '
                        'file will start building history.',
                        textAlign: TextAlign.center,
                      ),
                    ),
                  )
                else
                  Expanded(
                    child: ListView(
                      children: [
                        CheckboxListTile(
                          value: _selected.contains('current'),
                          onChanged: (_) => _toggleSelected('current'),
                          title: Text('Current (v${widget.composeFile.version})'),
                          subtitle: Text(
                            'Updated ${widget.composeFile.updatedAt.toLocal()}',
                          ),
                        ),
                        const Divider(height: 1),
                        for (final v in versions)
                          CheckboxListTile(
                            value: _selected.contains(v.id),
                            onChanged: (_) => _toggleSelected(v.id),
                            title: Text('v${v.versionNumber} — ${v.name}'),
                            subtitle: Text(v.createdAt.toLocal().toString()),
                            secondary: IconButton(
                              icon: _restoring
                                  ? const SizedBox(
                                      width: 16,
                                      height: 16,
                                      child: CircularProgressIndicator(
                                        strokeWidth: 2,
                                      ),
                                    )
                                  : const Icon(Icons.restore),
                              tooltip: 'Restore this version',
                              onPressed: _restoring ? null : () => _restore(v),
                            ),
                          ),
                      ],
                    ),
                  ),
                Padding(
                  padding: const EdgeInsets.all(12),
                  child: FilledButton.icon(
                    onPressed: _selected.length == 2
                        ? () => _compare(snapshots)
                        : null,
                    icon: const Icon(Icons.compare_arrows),
                    label: Text(
                      _selected.length == 2
                          ? 'Compare selected'
                          : 'Select 2 versions to compare',
                    ),
                  ),
                ),
              ],
            );
          },
        ),
    );
  }
}

enum _DiffOp { equal, insert, delete }

class _DiffLine {
  final _DiffOp op;
  final String text;
  const _DiffLine(this.op, this.text);
}

/// Line-level diff via a straightforward LCS dynamic-programming table —
/// more than adequate for a compose file's realistic size (tens to a few
/// hundred lines); not meant for arbitrarily large documents.
List<_DiffLine> _computeLineDiff(String oldText, String newText) {
  final a = oldText.split('\n');
  final b = newText.split('\n');
  final n = a.length;
  final m = b.length;
  final dp = List.generate(n + 1, (_) => List<int>.filled(m + 1, 0));
  for (var i = n - 1; i >= 0; i--) {
    for (var j = m - 1; j >= 0; j--) {
      dp[i][j] = a[i] == b[j]
          ? dp[i + 1][j + 1] + 1
          : (dp[i + 1][j] > dp[i][j + 1] ? dp[i + 1][j] : dp[i][j + 1]);
    }
  }

  final result = <_DiffLine>[];
  var i = 0, j = 0;
  while (i < n && j < m) {
    if (a[i] == b[j]) {
      result.add(_DiffLine(_DiffOp.equal, a[i]));
      i++;
      j++;
    } else if (dp[i + 1][j] >= dp[i][j + 1]) {
      result.add(_DiffLine(_DiffOp.delete, a[i]));
      i++;
    } else {
      result.add(_DiffLine(_DiffOp.insert, b[j]));
      j++;
    }
  }
  while (i < n) {
    result.add(_DiffLine(_DiffOp.delete, a[i]));
    i++;
  }
  while (j < m) {
    result.add(_DiffLine(_DiffOp.insert, b[j]));
    j++;
  }
  return result;
}

/// Renders a unified line diff between two Compose YAML texts, and offers
/// copying either side to the clipboard ("Export and download Compose
/// files" — this app shows/copies content rather than writing files to
/// disk, consistent with how container log download already works).
class ComposeDiffScreen extends StatelessWidget {
  final String oldLabel;
  final String oldContent;
  final String newLabel;
  final String newContent;

  const ComposeDiffScreen({
    super.key,
    required this.oldLabel,
    required this.oldContent,
    required this.newLabel,
    required this.newContent,
  });

  @override
  Widget build(BuildContext context) {
    final diff = _computeLineDiff(oldContent, newContent);
    final scheme = Theme.of(context).colorScheme;

    return Scaffold(
      appBar: AppBar(
        title: Text('$oldLabel  →  $newLabel'),
        actions: [
          IconButton(
            icon: const Icon(Icons.copy_outlined),
            tooltip: 'Copy $newLabel to clipboard',
            onPressed: () async {
              await Clipboard.setData(ClipboardData(text: newContent));
              if (context.mounted) {
                ScaffoldMessenger.of(context).showSnackBar(
                  SnackBar(content: Text('Copied $newLabel to clipboard.')),
                );
              }
            },
          ),
        ],
      ),
      body: diff.every((d) => d.op == _DiffOp.equal)
          ? const Center(child: Text('No differences.'))
          : ListView.builder(
              itemCount: diff.length,
              itemBuilder: (context, index) {
                final d = diff[index];
                final Color? bg;
                final String prefix;
                switch (d.op) {
                  case _DiffOp.insert:
                    bg = scheme.tertiaryContainer.withValues(alpha: 0.4);
                    prefix = '+ ';
                  case _DiffOp.delete:
                    bg = scheme.errorContainer.withValues(alpha: 0.4);
                    prefix = '- ';
                  case _DiffOp.equal:
                    bg = null;
                    prefix = '  ';
                }
                return Container(
                  color: bg,
                  padding: const EdgeInsets.symmetric(
                    horizontal: 16,
                    vertical: 2,
                  ),
                  child: Text(
                    '$prefix${d.text}',
                    style: const TextStyle(
                      fontFamily: 'monospace',
                      fontSize: 12,
                    ),
                  ),
                );
              },
            ),
    );
  }
}
