import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/user.dart';
import 'add_user_dialog.dart';
import 'reset_password_dialog.dart';

class UserListScreen extends StatefulWidget {
  final ApiClient apiClient;
  final String currentUserId;

  const UserListScreen({
    super.key,
    required this.apiClient,
    required this.currentUserId,
  });

  @override
  State<UserListScreen> createState() => _UserListScreenState();
}

class _UserListScreenState extends State<UserListScreen> {
  late Future<List<AppUser>> _usersFuture;

  @override
  void initState() {
    super.initState();
    _usersFuture = widget.apiClient.listUsers();
  }

  Future<void> _refresh() async {
    setState(() {
      _usersFuture = widget.apiClient.listUsers();
    });
    await _usersFuture;
  }

  Future<void> _openAddUser() async {
    final created = await showDialog<bool>(
      context: context,
      builder: (_) => AddUserDialog(apiClient: widget.apiClient),
    );
    if (created == true) await _refresh();
  }

  Future<void> _resetPassword(AppUser user) async {
    await showDialog<bool>(
      context: context,
      builder: (_) =>
          ResetPasswordDialog(apiClient: widget.apiClient, user: user),
    );
  }

  Future<void> _changeRole(AppUser user, String role) async {
    try {
      await widget.apiClient.updateUserRole(user.id, role);
      await _refresh();
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('Failed to change role: $e')));
    }
  }

  Future<void> _deleteUser(AppUser user) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (_) => AlertDialog(
        title: const Text('Delete user'),
        content: Text('Delete ${user.email}? This cannot be undone.'),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.of(context).pop(true),
            child: const Text('Delete'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;
    try {
      await widget.apiClient.deleteUser(user.id);
      await _refresh();
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('Failed to delete user: $e')));
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Users')),
      floatingActionButton: FloatingActionButton.extended(
        onPressed: _openAddUser,
        icon: const Icon(Icons.add),
        label: const Text('Add user'),
      ),
      body: RefreshIndicator(
        onRefresh: _refresh,
        child: FutureBuilder<List<AppUser>>(
          future: _usersFuture,
          builder: (context, snapshot) {
            if (snapshot.connectionState == ConnectionState.waiting) {
              return const Center(child: CircularProgressIndicator());
            }
            if (snapshot.hasError) {
              return ListView(
                children: [
                  Padding(
                    padding: const EdgeInsets.all(24),
                    child: Text('Failed to load users: ${snapshot.error}'),
                  ),
                ],
              );
            }
            final users = snapshot.data ?? [];
            return ListView.builder(
              itemCount: users.length,
              itemBuilder: (context, index) {
                final user = users[index];
                final isSelf = user.id == widget.currentUserId;
                return ListTile(
                  leading: Icon(
                    Icons.person,
                    color: user.isAdmin ? Colors.teal : Colors.grey,
                  ),
                  title: Text(user.email),
                  subtitle: Text(
                    '${user.role}${isSelf ? ' • you' : ''} • '
                    'created ${user.createdAt.toLocal()}',
                  ),
                  trailing: PopupMenuButton<String>(
                    onSelected: (action) {
                      switch (action) {
                        case 'make-admin':
                          _changeRole(user, 'admin');
                          break;
                        case 'make-viewer':
                          _changeRole(user, 'viewer');
                          break;
                        case 'reset-password':
                          _resetPassword(user);
                          break;
                        case 'delete':
                          _deleteUser(user);
                          break;
                      }
                    },
                    itemBuilder: (context) => [
                      if (user.role != 'admin')
                        const PopupMenuItem(
                          value: 'make-admin',
                          child: Text('Make admin'),
                        ),
                      if (user.role != 'viewer')
                        const PopupMenuItem(
                          value: 'make-viewer',
                          child: Text('Make viewer'),
                        ),
                      const PopupMenuItem(
                        value: 'reset-password',
                        child: Text('Reset password'),
                      ),
                      PopupMenuItem(
                        value: 'delete',
                        enabled: !isSelf,
                        child: const Text('Delete'),
                      ),
                    ],
                  ),
                );
              },
            );
          },
        ),
      ),
    );
  }
}
