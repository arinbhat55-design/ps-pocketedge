import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/server.dart';
import '../../models/user.dart';
import '../deployments/catalog_screen.dart';
import '../deployments/deployment_status_screen.dart';
import '../users/change_password_dialog.dart';
import '../users/user_list_screen.dart';
import 'add_server_dialog.dart';
import 'server_detail_screen.dart';

class ServerListScreen extends StatefulWidget {
  final ApiClient apiClient;
  final VoidCallback? onLogout;

  const ServerListScreen({super.key, required this.apiClient, this.onLogout});

  @override
  State<ServerListScreen> createState() => _ServerListScreenState();
}

class _ServerListScreenState extends State<ServerListScreen> {
  late Future<List<Server>> _serversFuture;
  late Future<AppUser> _meFuture;

  @override
  void initState() {
    super.initState();
    _serversFuture = widget.apiClient.listServers();
    _meFuture = widget.apiClient.getMe();
  }

  Future<void> _refresh() async {
    setState(() {
      _serversFuture = widget.apiClient.listServers();
    });
    await _serversFuture;
  }

  Future<void> _openDeploy(Server server) async {
    final deploymentId = await Navigator.of(context).push<String>(
      MaterialPageRoute(
        builder: (_) => CatalogScreen(
          apiClient: widget.apiClient,
          serverId: server.id,
          serverName: server.name,
        ),
      ),
    );
    if (deploymentId == null || !mounted) return;
    await Navigator.of(context).push(MaterialPageRoute(
      builder: (_) => DeploymentStatusScreen(
        apiClient: widget.apiClient,
        deploymentId: deploymentId,
      ),
    ));
  }

  Future<void> _openDetail(Server server) async {
    await Navigator.of(context).push(MaterialPageRoute(
      builder: (_) => ServerDetailScreen(
        apiClient: widget.apiClient,
        serverId: server.id,
        serverName: server.name,
      ),
    ));
  }

  Future<void> _openAddServer() async {
    await showDialog<void>(
      context: context,
      builder: (_) => AddServerDialog(apiClient: widget.apiClient),
    );
    // A freshly enrolled server won't show up until it's actually running
    // and has enrolled, so this refresh is best-effort, not guaranteed to
    // show the new server immediately.
    await _refresh();
  }

  Future<void> _openChangePassword() async {
    await showDialog<bool>(
      context: context,
      builder: (_) => ChangePasswordDialog(apiClient: widget.apiClient),
    );
  }

  Future<void> _openManageUsers(String currentUserId) async {
    await Navigator.of(context).push(MaterialPageRoute(
      builder: (_) => UserListScreen(
        apiClient: widget.apiClient,
        currentUserId: currentUserId,
      ),
    ));
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('Servers'),
        actions: [
          FutureBuilder<AppUser>(
            future: _meFuture,
            builder: (context, snapshot) {
              final me = snapshot.data;
              if (me == null) return const SizedBox.shrink();
              return Row(
                children: [
                  IconButton(
                    icon: const Icon(Icons.lock_outline),
                    tooltip: 'Change password',
                    onPressed: _openChangePassword,
                  ),
                  if (me.isAdmin)
                    IconButton(
                      icon: const Icon(Icons.people_outline),
                      tooltip: 'Manage users',
                      onPressed: () => _openManageUsers(me.id),
                    ),
                ],
              );
            },
          ),
          if (widget.onLogout != null)
            IconButton(
              icon: const Icon(Icons.logout),
              tooltip: 'Log out',
              onPressed: widget.onLogout,
            ),
        ],
      ),
      floatingActionButton: FloatingActionButton.extended(
        onPressed: _openAddServer,
        icon: const Icon(Icons.add),
        label: const Text('Add server'),
      ),
      body: RefreshIndicator(
        onRefresh: _refresh,
        child: FutureBuilder<List<Server>>(
          future: _serversFuture,
          builder: (context, snapshot) {
            if (snapshot.connectionState == ConnectionState.waiting) {
              return const Center(child: CircularProgressIndicator());
            }
            if (snapshot.hasError) {
              return ListView(
                children: [
                  Padding(
                    padding: const EdgeInsets.all(24),
                    child: Text('Failed to load servers: ${snapshot.error}'),
                  ),
                ],
              );
            }
            final servers = snapshot.data ?? [];
            if (servers.isEmpty) {
              return ListView(
                children: const [
                  Padding(
                    padding: EdgeInsets.all(24),
                    child: Text('No servers registered yet.'),
                  ),
                ],
              );
            }
            return ListView.builder(
              itemCount: servers.length,
              itemBuilder: (context, index) {
                final server = servers[index];
                return ListTile(
                  onTap: () => _openDetail(server),
                  leading: Icon(
                    Icons.dns,
                    color: server.status == 'online'
                        ? Colors.green
                        : Colors.grey,
                  ),
                  title: Text(server.name),
                  subtitle:
                      Text('${server.os}/${server.arch} • ${server.status}'),
                  trailing: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      if (server.lastResources != null) ...[
                        Text(
                          'CPU ${server.lastResources!.cpuPercent.toStringAsFixed(0)}% '
                          'MEM ${server.lastResources!.memPercent.toStringAsFixed(0)}%',
                        ),
                        const SizedBox(width: 8),
                      ],
                      FilledButton.tonalIcon(
                        onPressed: () => _openDeploy(server),
                        icon: const Icon(Icons.rocket_launch, size: 18),
                        label: const Text('Deploy'),
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
