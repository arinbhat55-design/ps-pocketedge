import 'dart:convert';

import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/compose_file.dart';
import 'compose_visual_editor.dart';

enum _Mode { visual, yaml }

/// Create or edit one Compose file, in either a form-based visual editor or
/// a raw YAML editor — the two "Compose file management" editing modes
/// from the Deployment Management spec. Visual mode always round-trips
/// through the control plane (POST .../render and .../parse) rather than
/// converting locally, so the app never needs its own YAML parser.
class ComposeEditorScreen extends StatefulWidget {
  final ApiClient apiClient;
  final ComposeFile? existing;

  const ComposeEditorScreen({super.key, required this.apiClient, this.existing});

  @override
  State<ComposeEditorScreen> createState() => _ComposeEditorScreenState();
}

class _ComposeEditorScreenState extends State<ComposeEditorScreen> {
  late final TextEditingController _nameController;
  late final TextEditingController _yamlController;

  final _visualKey = GlobalKey<ComposeVisualEditorState>();
  List<ComposeServiceDraft> _visualServices = const [];
  int _visualGeneration = 0;

  late _Mode _mode;
  bool _saving = false;
  bool _busy = false;
  List<String> _yamlErrors = [];
  // Conflicting/unsupported settings from the last validation — shown but
  // don't block saving.
  List<String> _yamlWarnings = [];
  String? _error;

  bool get _isNew => widget.existing == null;

  @override
  void initState() {
    super.initState();
    _nameController = TextEditingController(text: widget.existing?.name ?? '');
    _yamlController = TextEditingController(text: widget.existing?.content ?? '');
    _mode = _isNew ? _Mode.visual : _Mode.yaml;
  }

  @override
  void dispose() {
    _nameController.dispose();
    _yamlController.dispose();
    super.dispose();
  }

  List<String> _extractErrors(Object error) {
    if (error is ApiException) {
      try {
        final decoded = jsonDecode(error.message);
        if (decoded is Map && decoded['errors'] is List) {
          return (decoded['errors'] as List).map((e) => e.toString()).toList();
        }
      } catch (_) {
        // Not JSON (e.g. a plain-text 409/500 body) — fall through.
      }
      return [error.message];
    }
    return [error.toString()];
  }

  Future<void> _validateYaml() async {
    setState(() => _busy = true);
    try {
      final result = await widget.apiClient.parseComposeYaml(_yamlController.text);
      setState(() {
        _yamlErrors = result.valid ? [] : result.errors;
        _yamlWarnings = result.warnings;
      });
      if (result.valid && mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(
              result.warnings.isEmpty
                  ? 'YAML is valid.'
                  : 'YAML is valid, with ${result.warnings.length} warning(s).',
            ),
          ),
        );
      }
    } catch (e) {
      setState(() => _yamlErrors = _extractErrors(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _switchToYaml() async {
    final services = _visualKey.currentState?.currentServices() ?? _visualServices;
    if (services.isEmpty) {
      setState(() => _mode = _Mode.yaml);
      return;
    }
    setState(() => _busy = true);
    try {
      final content = await widget.apiClient.renderComposeYaml(services);
      setState(() {
        _yamlController.text = content;
        _yamlErrors = [];
        _mode = _Mode.yaml;
      });
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(_extractErrors(e).join('\n'))),
        );
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _switchToVisual() async {
    setState(() => _busy = true);
    ComposeParseResult result;
    try {
      result = await widget.apiClient.parseComposeYaml(_yamlController.text);
    } catch (e) {
      setState(() {
        _busy = false;
        _yamlErrors = _extractErrors(e);
      });
      return;
    }
    setState(() => _busy = false);

    if (!result.valid) {
      setState(() => _yamlErrors = result.errors);
      return;
    }

    if (!result.visualEditable) {
      if (!mounted) return;
      final proceed = await showDialog<bool>(
        context: context,
        builder: (_) => AlertDialog(
          title: const Text("Some settings aren't shown in Visual mode"),
          content: const Text(
            'This file uses Compose settings the visual editor cannot display '
            '(e.g. build, networks, healthcheck). Switching will hide them, '
            'and saving from Visual mode will remove them from the file.',
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(context).pop(false),
              child: const Text('Stay in YAML'),
            ),
            FilledButton(
              onPressed: () => Navigator.of(context).pop(true),
              child: const Text('Switch anyway'),
            ),
          ],
        ),
      );
      if (proceed != true) return;
    }

    setState(() {
      _visualServices = result.services;
      _visualGeneration++;
      _yamlErrors = [];
      _mode = _Mode.visual;
    });
  }

  Future<void> _save() async {
    final name = _nameController.text.trim();
    if (name.isEmpty) {
      setState(() => _error = 'Name is required.');
      return;
    }

    String content;
    if (_mode == _Mode.visual) {
      final services = _visualKey.currentState?.currentServices() ?? const [];
      try {
        content = await widget.apiClient.renderComposeYaml(services);
      } catch (e) {
        setState(() => _error = _extractErrors(e).join('\n'));
        return;
      }
    } else {
      content = _yamlController.text;
    }

    setState(() {
      _saving = true;
      _error = null;
    });
    try {
      if (_isNew) {
        await widget.apiClient.createComposeFile(name: name, content: content);
      } else {
        await widget.apiClient.updateComposeFile(
          widget.existing!.id,
          name: name,
          content: content,
        );
      }
      if (mounted) Navigator.of(context).pop(true);
    } catch (e) {
      setState(() => _error = _extractErrors(e).join('\n'));
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(_isNew ? 'New Compose file' : 'Edit ${widget.existing!.name}'),
        actions: [
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 8),
            child: SegmentedButton<_Mode>(
              segments: const [
                ButtonSegment(
                  value: _Mode.visual,
                  label: Text('Visual'),
                  icon: Icon(Icons.dashboard_customize_outlined),
                ),
                ButtonSegment(
                  value: _Mode.yaml,
                  label: Text('YAML'),
                  icon: Icon(Icons.code),
                ),
              ],
              selected: {_mode},
              onSelectionChanged: _busy
                  ? null
                  : (selection) {
                      final next = selection.first;
                      if (next == _mode) return;
                      if (next == _Mode.yaml) {
                        _switchToYaml();
                      } else {
                        _switchToVisual();
                      }
                    },
            ),
          ),
        ],
      ),
      floatingActionButton: FloatingActionButton.extended(
        onPressed: _saving ? null : _save,
        icon: _saving
            ? const SizedBox(
                width: 16,
                height: 16,
                child: CircularProgressIndicator(strokeWidth: 2),
              )
            : const Icon(Icons.save_outlined),
        label: const Text('Save'),
      ),
      body: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 16, 16, 0),
            child: TextField(
              controller: _nameController,
              decoration: const InputDecoration(
                labelText: 'Stack name',
                hintText: 'e.g. blog-platform',
                border: OutlineInputBorder(),
                isDense: true,
              ),
            ),
          ),
          if (_error != null)
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 12, 16, 0),
              child: Text(_error!, style: const TextStyle(color: Colors.red)),
            ),
          if (_mode == _Mode.yaml && _yamlErrors.isNotEmpty)
            Container(
              margin: const EdgeInsets.fromLTRB(16, 12, 16, 0),
              padding: const EdgeInsets.all(12),
              decoration: BoxDecoration(
                color: Theme.of(context).colorScheme.errorContainer,
                borderRadius: BorderRadius.circular(8),
              ),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    'Validation errors',
                    style: Theme.of(context).textTheme.titleSmall?.copyWith(
                      color: Theme.of(context).colorScheme.onErrorContainer,
                    ),
                  ),
                  const SizedBox(height: 4),
                  for (final e in _yamlErrors)
                    Text(
                      '• $e',
                      style: TextStyle(
                        color: Theme.of(context).colorScheme.onErrorContainer,
                      ),
                    ),
                ],
              ),
            ),
          if (_mode == _Mode.yaml && _yamlWarnings.isNotEmpty)
            Container(
              margin: const EdgeInsets.fromLTRB(16, 12, 16, 0),
              padding: const EdgeInsets.all(12),
              decoration: BoxDecoration(
                color: Colors.orange.withValues(alpha: 0.12),
                borderRadius: BorderRadius.circular(8),
              ),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    'Unsupported or conflicting settings',
                    style: Theme.of(context).textTheme.titleSmall,
                  ),
                  const SizedBox(height: 4),
                  for (final w in _yamlWarnings) Text('• $w'),
                ],
              ),
            ),
          const SizedBox(height: 8),
          Expanded(
            child: _mode == _Mode.visual
                ? ComposeVisualEditor(
                    key: ValueKey(_visualGeneration),
                    initialServices: _visualServices,
                    onChanged: (services) => _visualServices = services,
                  )
                : Padding(
                    padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.stretch,
                      children: [
                        Align(
                          alignment: Alignment.centerRight,
                          child: TextButton.icon(
                            onPressed: _busy ? null : _validateYaml,
                            icon: const Icon(Icons.check_circle_outline),
                            label: const Text('Validate'),
                          ),
                        ),
                        Expanded(
                          child: Container(
                            decoration: BoxDecoration(
                              border: Border.all(
                                color: Theme.of(context).dividerColor,
                              ),
                              borderRadius: BorderRadius.circular(8),
                            ),
                            padding: const EdgeInsets.all(8),
                            child: TextField(
                              controller: _yamlController,
                              maxLines: null,
                              expands: true,
                              textAlignVertical: TextAlignVertical.top,
                              style: const TextStyle(
                                fontFamily: 'monospace',
                                fontSize: 13,
                              ),
                              decoration: const InputDecoration(
                                border: InputBorder.none,
                                hintText: 'services:\n  web:\n    image: nginx:latest\n',
                              ),
                            ),
                          ),
                        ),
                      ],
                    ),
                  ),
          ),
        ],
      ),
    );
  }
}
