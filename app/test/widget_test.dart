import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;

import 'package:app/api/api_client.dart';
import 'package:app/widgets/state_message.dart';
import 'package:app/features/servers/server_list_screen.dart';

void main() {
  testWidgets('server list screen shows empty state when no servers exist', (
    WidgetTester tester,
  ) async {
    final client = ApiClient(
      baseUrl: 'http://localhost:8080',
      httpClient: _FakeEmptyListClient(),
    );

    await tester.pumpWidget(
      MaterialApp(home: ServerListScreen(apiClient: client)),
    );
    await tester.pumpAndSettle();

    expect(find.text('No servers yet'), findsOneWidget);
    // The empty state offers the next step, not just a message.
    expect(
      find.descendant(
        of: find.byType(StateMessage),
        matching: find.widgetWithText(FilledButton, 'Add server'),
      ),
      findsOneWidget,
    );
  });
}

/// Fakes the control plane's GET /api/servers with an empty list, so this
/// test doesn't depend on a running control plane.
class _FakeEmptyListClient extends http.BaseClient {
  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    final body = utf8.encode('[]');
    return http.StreamedResponse(Stream.value(body), 200);
  }
}
