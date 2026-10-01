import 'package:flutter/material.dart';

import '../../api/api_client.dart';

class SettingsScreen extends StatefulWidget {
  final ApiClient apiClient;
  final ValueChanged<bool>? onAccessModeChanged;

  const SettingsScreen({
    super.key,
    required this.apiClient,
    this.onAccessModeChanged,
  });

  @override
  State<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends State<SettingsScreen> {
  late Future<Map<String, dynamic>> _settings = widget.apiClient
      .getAccessSettings();
  bool _saving = false;

  Future<void> _setRequireLogin(bool required) async {
    String? password;
    if (required) {
      var entered = '';
      password = await showDialog<String>(
        context: context,
        builder: (context) => AlertDialog(
          title: const Text('Require login on this computer?'),
          content: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Text(
                'Enter your admin password to confirm you can sign in. '
                'If you do not know it, reset your password in Users first.',
              ),
              const SizedBox(height: 12),
              TextField(
                autofocus: true,
                obscureText: true,
                decoration: const InputDecoration(labelText: 'Admin password'),
                onChanged: (value) => entered = value,
                onSubmitted: (_) => Navigator.pop(context, entered),
              ),
            ],
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(context),
              child: const Text('Cancel'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(context, entered),
              child: const Text('Require login'),
            ),
          ],
        ),
      );
      if (password == null) return;
    }

    setState(() => _saving = true);
    try {
      final updated = await widget.apiClient.updateAccessSettings(
        requireLocalLogin: required,
        password: password,
      );
      if (!mounted) return;
      if (updated['localListener'] == true &&
          widget.onAccessModeChanged != null) {
        widget.onAccessModeChanged!(required);
      } else {
        setState(() => _settings = Future.value(updated));
      }
    } catch (error) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Could not change login setting: $error')),
        );
      }
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: const Text('Settings')),
    body: FutureBuilder<Map<String, dynamic>>(
      future: _settings,
      builder: (context, snapshot) {
        if (!snapshot.hasData) {
          return Center(
            child: Text(
              snapshot.hasError
                  ? 'Could not load settings: ${snapshot.error}'
                  : 'Loading settings…',
            ),
          );
        }
        final localListener = snapshot.data!['localListener'] == true;
        final requireLogin = snapshot.data!['requireLocalLogin'] == true;
        return ListView(
          padding: const EdgeInsets.all(24),
          children: [
            Text('Access', style: Theme.of(context).textTheme.titleLarge),
            const SizedBox(height: 8),
            SwitchListTile(
              title: const Text('Require login on this computer'),
              subtitle: Text(
                localListener
                    ? 'Off: anyone using this computer can manage every '
                          'connected server without signing in, like Docker '
                          'Desktop. Turn this on if other people use this '
                          'computer. On: everyone must sign in.'
                    : 'This server listens on a network address, so login is '
                          'always required. Local access can only be configured '
                          'when the server listens on localhost.',
              ),
              value: requireLogin,
              onChanged: localListener && !_saving ? _setRequireLogin : null,
            ),
          ],
        );
      },
    ),
  );
}
