import 'dart:io';

import 'package:path_provider/path_provider.dart';

Future<String> downloadQueryCsv(String csv, String filename) async {
  final directory =
      await getDownloadsDirectory() ?? await getApplicationDocumentsDirectory();
  final path = '${directory.path}${Platform.pathSeparator}$filename';
  await File(path).writeAsString(csv, flush: true);
  return path;
}
