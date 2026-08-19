import 'package:flutter/material.dart';

import 'api/api_client.dart';
import 'features/servers/server_list_screen.dart';

/// Control-plane REST API base URL. Override at build/run time with
/// `--dart-define=CONTROL_PLANE_URL=http://host:port`
const controlPlaneUrl = String.fromEnvironment(
  'CONTROL_PLANE_URL',
  defaultValue: 'http://localhost:8080',
);

void main() {
  runApp(const PSPocketEdgeApp());
}

class PSPocketEdgeApp extends StatelessWidget {
  const PSPocketEdgeApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'PSpocketEdge',
      theme: ThemeData(colorScheme: ColorScheme.fromSeed(seedColor: Colors.teal)),
      home: ServerListScreen(apiClient: ApiClient(baseUrl: controlPlaneUrl)),
    );
  }
}
