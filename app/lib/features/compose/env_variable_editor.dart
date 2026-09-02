import 'package:flutter/material.dart';

import '../../models/env_var_group.dart';

class _Row {
  final TextEditingController key;
  final TextEditingController label;
  final TextEditingController value;
  bool secret;

  _Row(EnvVariable v)
    : key = TextEditingController(text: v.key),
      label = TextEditingController(text: v.label),
      value = TextEditingController(text: v.value),
      secret = v.secret;

  EnvVariable toVariable() => EnvVariable(
    key: key.text.trim(),
    label: label.text.trim(),
    value: value.text,
    secret: secret,
  );

  void dispose() {
    key.dispose();
    label.dispose();
    value.dispose();
  }
}

/// Form editor for a list of key/value/secret environment variables —
/// "Environment variable editor". Shared by the env var group management
/// screen and the ad-hoc editor offered at deploy time.
class EnvVariableEditor extends StatefulWidget {
  final List<EnvVariable> initialVariables;
  final ValueChanged<List<EnvVariable>>? onChanged;

  const EnvVariableEditor({
    super.key,
    this.initialVariables = const [],
    this.onChanged,
  });

  @override
  State<EnvVariableEditor> createState() => EnvVariableEditorState();
}

class EnvVariableEditorState extends State<EnvVariableEditor> {
  late List<_Row> _rows;

  @override
  void initState() {
    super.initState();
    _rows = [for (final v in widget.initialVariables) _Row(v)];
  }

  @override
  void dispose() {
    for (final r in _rows) {
      r.dispose();
    }
    super.dispose();
  }

  List<EnvVariable> currentVariables() =>
      [for (final r in _rows) r.toVariable()].where((v) => v.key.isNotEmpty).toList();

  void _notify() => widget.onChanged?.call(currentVariables());

  void _add() {
    setState(() => _rows.add(_Row(const EnvVariable(key: ''))));
    _notify();
  }

  void _remove(int index) {
    setState(() => _rows.removeAt(index).dispose());
    _notify();
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      mainAxisSize: MainAxisSize.min,
      children: [
        Row(
          mainAxisAlignment: MainAxisAlignment.spaceBetween,
          children: [
            Text(
              'Environment variables',
              style: Theme.of(context).textTheme.titleSmall,
            ),
            IconButton(
              icon: const Icon(Icons.add_circle_outline),
              tooltip: 'Add variable',
              visualDensity: VisualDensity.compact,
              onPressed: _add,
            ),
          ],
        ),
        if (_rows.isEmpty)
          const Padding(
            padding: EdgeInsets.symmetric(vertical: 8),
            child: Text('No variables yet.'),
          ),
        for (var i = 0; i < _rows.length; i++)
          Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.center,
              children: [
                Expanded(
                  flex: 2,
                  child: TextField(
                    controller: _rows[i].key,
                    onChanged: (_) => _notify(),
                    decoration: const InputDecoration(
                      hintText: 'KEY',
                      isDense: true,
                    ),
                  ),
                ),
                const SizedBox(width: 8),
                Expanded(
                  flex: 3,
                  child: TextField(
                    controller: _rows[i].value,
                    onChanged: (_) => _notify(),
                    obscureText: _rows[i].secret,
                    decoration: const InputDecoration(
                      hintText: 'value',
                      isDense: true,
                    ),
                  ),
                ),
                IconButton(
                  icon: Icon(
                    _rows[i].secret
                        ? Icons.visibility_off_outlined
                        : Icons.visibility_outlined,
                  ),
                  tooltip: _rows[i].secret ? 'Marked as secret' : 'Mark as secret',
                  visualDensity: VisualDensity.compact,
                  onPressed: () {
                    setState(() => _rows[i].secret = !_rows[i].secret);
                    _notify();
                  },
                ),
                IconButton(
                  icon: const Icon(Icons.remove_circle_outline),
                  visualDensity: VisualDensity.compact,
                  onPressed: () => _remove(i),
                ),
              ],
            ),
          ),
      ],
    );
  }
}
