import 'dart:convert';
import 'dart:typed_data';

import 'package:file_picker/file_picker.dart';
import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/image.dart' show formatBytes;
import '../../models/volume.dart';

class VolumeFilesScreen extends StatefulWidget {
  final ApiClient apiClient;
  final VolumeSummary volume;
  final bool isAdmin;

  const VolumeFilesScreen({
    super.key,
    required this.apiClient,
    required this.volume,
    required this.isAdmin,
  });

  @override
  State<VolumeFilesScreen> createState() => _VolumeFilesScreenState();
}

class _VolumeFilesScreenState extends State<VolumeFilesScreen> {
  String _path = '';
  late Future<List<VolumeFileEntry>> _files = _load();
  bool _busy = false;

  bool get _canWrite => widget.isAdmin && widget.volume.orphaned;

  Future<List<VolumeFileEntry>> _load() => widget.apiClient.listVolumeFiles(
    widget.volume.serverId,
    widget.volume.name,
    path: _path,
  );

  void _refresh() => setState(() => _files = _load());

  void _message(String text) {
    if (mounted) {
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(text)));
    }
  }

  String _join(String name) => _path.isEmpty ? name : '$_path/$name';

  void _openDirectory(String name) {
    setState(() {
      _path = _join(name);
      _files = _load();
    });
  }

  void _goUp() {
    if (_path.isEmpty) return;
    setState(() {
      final split = _path.lastIndexOf('/');
      _path = split < 0 ? '' : _path.substring(0, split);
      _files = _load();
    });
  }

  Future<void> _saveToDevice(String name, List<int> bytes) async {
    try {
      await FilePicker.saveFile(
        dialogTitle: 'Save $name',
        fileName: name,
        bytes: Uint8List.fromList(bytes),
      );
    } catch (error) {
      _message('Could not save file: $error');
    }
  }

  Future<void> _openFile(VolumeFileEntry entry) async {
    final filePath = _join(entry.name);
    List<int> bytes;
    try {
      bytes = await widget.apiClient.readVolumeFile(
        widget.volume.serverId,
        widget.volume.name,
        filePath,
      );
    } catch (error) {
      _message('Could not read file: $error');
      return;
    }
    String? text;
    try {
      text = utf8.decode(bytes);
      if (text.contains('\x00')) text = null;
    } on FormatException {
      text = null;
    }
    if (!mounted) return;
    var edited = text;
    final saved = await showDialog<String>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(entry.name),
        content: SizedBox(
          width: 640,
          height: 360,
          child: text == null
              ? const Center(
                  child: Text('Binary file. Download it to view it.'),
                )
              : _canWrite
              ? TextFormField(
                  initialValue: text,
                  maxLines: null,
                  expands: true,
                  decoration: const InputDecoration(
                    border: OutlineInputBorder(),
                  ),
                  onChanged: (value) => edited = value,
                )
              : SingleChildScrollView(child: SelectableText(text)),
        ),
        actions: [
          TextButton(
            onPressed: () => _saveToDevice(entry.name, bytes),
            child: const Text('Download'),
          ),
          TextButton(
            onPressed: () => Navigator.pop(context),
            child: const Text('Close'),
          ),
          if (text != null && _canWrite)
            FilledButton(
              onPressed: () => Navigator.pop(context, edited),
              child: const Text('Save'),
            ),
        ],
      ),
    );
    if (saved == null) return;
    try {
      await widget.apiClient.writeVolumeFile(
        widget.volume.serverId,
        widget.volume.name,
        filePath,
        utf8.encode(saved),
      );
      _message('Saved $filePath');
      _refresh();
    } catch (error) {
      _message('Could not save file: $error');
    }
  }

  Future<void> _upload() async {
    final file = await FilePicker.pickFile();
    if (file == null) return;
    final size = await file.length();
    if (size == null ||
        size > 1 << 20 ||
        file.name.contains('/') ||
        file.name.contains('\\')) {
      _message('Choose a file of at most 1 MiB with a simple name.');
      return;
    }
    setState(() => _busy = true);
    try {
      final bytes = await file.readAsBytes();
      if (bytes.length > 1 << 20) {
        throw const FormatException('File exceeds 1 MiB');
      }
      await widget.apiClient.writeVolumeFile(
        widget.volume.serverId,
        widget.volume.name,
        _join(file.name),
        bytes,
      );
      _message('Uploaded ${file.name}');
      _refresh();
    } catch (error) {
      _message('Upload failed: $error');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(
      title: Text('${widget.volume.name} / ${_path.isEmpty ? '' : _path}'),
      actions: [
        if (_canWrite)
          IconButton(
            onPressed: _busy ? null : _upload,
            icon: const Icon(Icons.upload_file_outlined),
            tooltip: 'Upload file (1 MiB max)',
          ),
        IconButton(
          onPressed: _refresh,
          icon: const Icon(Icons.refresh),
          tooltip: 'Refresh',
        ),
      ],
    ),
    body: FutureBuilder<List<VolumeFileEntry>>(
      future: _files,
      builder: (context, snapshot) {
        if (!snapshot.hasData) {
          return Center(
            child: Text(
              snapshot.hasError
                  ? 'Could not list files: ${snapshot.error}'
                  : 'Loading files…',
            ),
          );
        }
        final files = snapshot.data!
          ..sort((a, b) {
            if (a.isDirectory != b.isDirectory) return a.isDirectory ? -1 : 1;
            return a.name.compareTo(b.name);
          });
        return ListView(
          children: [
            if (_path.isNotEmpty)
              ListTile(
                leading: const Icon(Icons.arrow_upward),
                title: const Text('..'),
                onTap: _goUp,
              ),
            if (files.isEmpty)
              const ListTile(title: Text('This directory is empty.')),
            for (final entry in files)
              ListTile(
                leading: Icon(
                  entry.isSymlink
                      ? Icons.link
                      : entry.isDirectory
                      ? Icons.folder_outlined
                      : Icons.insert_drive_file_outlined,
                ),
                title: Text(entry.name),
                subtitle: Text(
                  entry.isSymlink
                      ? 'Symlink (not opened)'
                      : entry.isDirectory
                      ? 'Folder'
                      : formatBytes(entry.sizeBytes),
                ),
                onTap: entry.isSymlink
                    ? null
                    : entry.isDirectory
                    ? () => _openDirectory(entry.name)
                    : () => _openFile(entry),
              ),
          ],
        );
      },
    ),
  );
}
