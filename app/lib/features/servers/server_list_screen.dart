import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/server.dart';

class ServerListScreen extends StatefulWidget {
  final ApiClient apiClient;

  const ServerListScreen({super.key, required this.apiClient});

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

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Servers')),
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
