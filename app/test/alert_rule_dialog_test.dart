import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;

import 'package:app/api/api_client.dart';
import 'package:app/features/alerts/alert_rule_dialog.dart';

void main() {
  testWidgets('CPU threshold is entered in cores and sent as percent', (
    tester,
  ) async {
    final fake = _FakeClient();
    await tester.pumpWidget(
      MaterialApp(
        home: Builder(
          builder: (context) => TextButton(
            onPressed: () => showAlertRuleDialog(
              context,
              apiClient: ApiClient(
                baseUrl: 'http://localhost:8080',
                httpClient: fake,
              ),
              servers: const {},
            ),
            child: const Text('open'),
          ),
        ),
      ),
    );
    await tester.tap(find.text('open'));
    await tester.pumpAndSettle();

    expect(find.text('cores'), findsOneWidget);
    await tester.enterText(find.widgetWithText(TextField, 'Name'), 'Hot');
    await tester.enterText(find.widgetWithText(TextField, '0.9'), '1.5');
    await tester.tap(find.text('Save'));
    await tester.pumpAndSettle();

    expect(fake.body?['metric'], 'cpu');
    expect(fake.body?['threshold'], 150);
  });
}

class _FakeClient extends http.BaseClient {
  Map<String, dynamic>? body;

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    body = jsonDecode((request as http.Request).body) as Map<String, dynamic>;
    return http.StreamedResponse(
      Stream.value(utf8.encode(jsonEncode({'id': 'r1'}))),
      201,
    );
  }
}
