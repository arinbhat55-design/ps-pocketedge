import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/user.dart';
import 'add_user_dialog.dart';
import 'reset_password_dialog.dart';
import '../../theme/app_theme.dart';
import '../../widgets/page_intro.dart';
import '../../widgets/state_message.dart';

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
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(SnackBar(content: Text('Failed to change role: $e')));
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
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(SnackBar(content: Text('Failed to delete user: $e')));
    }
  }

  @override
  Widget build(BuildContext context) {
    final addUser = PrimaryAction(
      label: 'Add user',
      icon: Icons.person_add_alt,
      onPressed: _openAddUser,
    );
    return Scaffold(
      appBar: AppBar(
        title: const Text('Users'),
        actions: [?addUser.appBarAction(context)],
      ),
      floatingActionButton: addUser.fab(context),
      body: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          const PageIntro(
            description:
                'People who can sign in, and their roles. Admins can '
                'manage users.',
          ),
          const SizedBox(height: Space.sm),
          Expanded(
            child: RefreshIndicator(
              onRefresh: _refresh,
              child: FutureBuilder<List<AppUser>>(
                future: _usersFuture,
                builder: (context, snapshot) {
                  if (snapshot.connectionState == ConnectionState.waiting) {
                    return const Center(child: CircularProgressIndicator());
                  }
                  if (snapshot.hasError) {
                    return StateMessage.error(
                      what: 'users',
                      error: snapshot.error,
                      onRetry: _refresh,
                    );
                  }
                  final users = snapshot.data ?? [];
                  return ListView.builder(
                    itemCount: users.length,
                    itemBuilder: (context, index) {
                      final user = users[index];
                      final isSelf = user.id == widget.currentUserId;
                      final joined = user.createdAt
                          .toLocal()
                          .toString()
                          .split(' ')
                          .first;
                      return ListTile(
                        leading: CircleAvatar(
                          radius: 16,
                          backgroundColor: AppColors.surfaceHighest,
                          foregroundColor: AppColors.textSecondary,
                          child: Text(
                            user.email.isEmpty
                                ? '?'
                                : user.email[0].toUpperCase(),
                          ),
                        ),
                        title: Text(
                          user.email,
                          style: Theme.of(context).textTheme.titleSmall,
                        ),
                        subtitle: Text(
                          [
                            user.isAdmin ? 'Admin' : 'Viewer',
                            if (isSelf) 'you',
                            'joined $joined',
                          ].join('  ·  '),
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
          ),
        ],
      ),
    );
  }
}
