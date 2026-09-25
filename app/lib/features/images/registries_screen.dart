import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/image.dart';
import '../../theme/app_theme.dart';

/// Admin-only CRUD for private registry credentials, used for pulling
/// from/searching private registries elsewhere in the Images module.
class RegistriesScreen extends StatefulWidget {
  final ApiClient apiClient;

  const RegistriesScreen({super.key, required this.apiClient});

  @override
  State<RegistriesScreen> createState() => _RegistriesScreenState();
}

class _RegistriesScreenState extends State<RegistriesScreen> {
  late Future<List<Registry>> _registriesFuture;

  @override
  void initState() {
    super.initState();
    _registriesFuture = widget.apiClient.listRegistries();
  }

  void _refresh() {
    setState(() {
      _registriesFuture = widget.apiClient.listRegistries();
    });
  }

  Future<void> _addRegistry() async {
    final nameController = TextEditingController();
    final urlController = TextEditingController();
    final usernameController = TextEditingController();
    final passwordController = TextEditingController();

    final saved = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Add registry'),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            TextField(
              controller: nameController,
              decoration: const InputDecoration(labelText: 'Name'),
            ),
            TextField(
              controller: urlController,
              decoration: const InputDecoration(
                labelText: 'URL',
                hintText: 'registry.example.com',
              ),
            ),
            TextField(
              controller: usernameController,
              decoration: const InputDecoration(labelText: 'Username'),
            ),
            TextField(
              controller: passwordController,
              decoration: const InputDecoration(labelText: 'Password'),
              obscureText: true,
            ),
          ],
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.of(context).pop(true),
            child: const Text('Add'),
          ),
        ],
      ),
    );

    if (saved != true) return;
    if (nameController.text.trim().isEmpty || urlController.text.trim().isEmpty) {
      return;
    }

    try {
      await widget.apiClient.createRegistry(
        name: nameController.text.trim(),
        url: urlController.text.trim(),
        username: usernameController.text.trim(),
        password: passwordController.text,
      );
      _refresh();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Failed to add registry: $e')));
      }
    }
  }

  Future<void> _deleteRegistry(Registry registry) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (_) => AlertDialog(
        title: Text('Remove ${registry.name}?'),
        content: const Text(
          'Pulls and searches configured against this registry will stop working.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(backgroundColor: AppColors.failed),
            onPressed: () => Navigator.of(context).pop(true),
            child: const Text('Remove'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;

    try {
      await widget.apiClient.deleteRegistry(registry.id);
      _refresh();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Failed to remove registry: $e')));
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      floatingActionButton: FloatingActionButton.extended(
        onPressed: _addRegistry,
        icon: const Icon(Icons.add),
        label: const Text('Add registry'),
      ),
      body: FutureBuilder<List<Registry>>(
        future: _registriesFuture,
        builder: (context, snapshot) {
          if (snapshot.connectionState == ConnectionState.waiting) {
            return const Center(child: CircularProgressIndicator());
          }
          if (snapshot.hasError) {
            return Center(
              child: Text('Failed to load registries: ${snapshot.error}'),
            );
          }
          final registries = snapshot.data ?? [];
          if (registries.isEmpty) {
            return const Center(
              child: Text('No private registries configured.'),
            );
          }
          return ListView(
            children: [
              for (final r in registries)
                ListTile(
                  leading: const Icon(Icons.storage),
                  title: Text(r.name),
                  subtitle: Text(
                    r.username.isEmpty ? r.url : '${r.url} (${r.username})',
                  ),
                  trailing: IconButton(
                    icon: const Icon(Icons.delete_outline),
                    onPressed: () => _deleteRegistry(r),
                  ),
                ),
            ],
          );
        },
      ),
    );
  }
}
