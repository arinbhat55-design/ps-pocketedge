import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/server.dart';
import 'add_server_dialog.dart';

class ServerListScreen extends StatefulWidget {
  final ApiClient apiClient;
  final VoidCallback? onLogout;

  const ServerListScreen({super.key, required this.apiClient, this.onLogout});

  @override
  State<ServerListScreen> createState() => _ServerListScreenState();
}

class _ServerListScreenState extends State<ServerListScreen> {
  late Future<List<Server>> _serversFuture;

  @override
  void initState() {
    super.initState();
    _serversFuture = widget.apiClient.listServers();
  }

  Future<void> _refresh() async {
    setState(() {
      _serversFuture = widget.apiClient.listServers();
    });
    await _serversFuture;
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

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('Servers'),
        actions: [
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
                  leading: Icon(
                    Icons.dns,
                    color: server.status == 'online'
                        ? Colors.green
                        : Colors.grey,
                  ),
                  title: Text(server.name),
                  subtitle: Text(
                      '${server.os}/${server.arch} • ${server.status}'),
                  trailing: server.lastResources == null
                      ? null
                      : Text(
                          'CPU ${server.lastResources!.cpuPercent.toStringAsFixed(0)}%',
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
