// Fork addition: the CSV export of the user audit panel, made in the browser
// from the rows fetched. The rows hold text from requests (attempted names,
// paths, token names): a cell that a spreadsheet would read as a formula is
// neutralised (OWASP "CSV Injection"), and every cell is quoted (RFC 4180).

// At most this many rows are exported (10 pages of the API's 200).
export const EXPORT_MAX_ROWS = 2000;
export const EXPORT_PAGE_SIZE = 200;

// A cell starting with one of these is a formula to Excel, LibreOffice or
// Google Sheets: =, +, -, @, tab, carriage return, line feed, and their
// full-width forms.
const FORMULA_START = /^[=+\-@\t\r\n＝＋－＠]/;

// csvCell quotes a value for CSV and neutralises a formula by a leading '.
export function csvCell(value) {
  let text = value === undefined || value === null ? '' : String(value);
  if (FORMULA_START.test(text)) text = "'" + text;
  return '"' + text.replace(/"/g, '""') + '"';
}

export function csvLine(values) {
  return values.map(csvCell).join(',');
}

export const CSV_COLUMNS = [
  'time_utc', 'time_local', 'zone', 'what', 'kind', 'user', 'target', 'outcome',
  'address', 'method', 'path', 'host', 'count', 'details', 'id',
];

function detailsText(details) {
  if (!details || typeof details !== 'object') return '';
  return Object.keys(details).sort().map((key) => key + '=' + details[key]).join('; ');
}

// auditCsv builds the file: a header and one line per row, CRLF line ends,
// with a byte order mark so that spreadsheets read UTF-8.
// options: sentence(row) -> text, formatTime(time) -> local text, zone.
export function auditCsv(rows, { sentence = () => '', formatTime = () => '', zone = '' } = {}) {
  const lines = [csvLine(CSV_COLUMNS)];
  for (const row of (rows || []).slice(0, EXPORT_MAX_ROWS)) {
    if (!row || typeof row !== 'object') continue;
    lines.push(csvLine([
      row.time || '',
      formatTime(row.time),
      zone,
      sentence(row),
      row.kind || '',
      row.actor || '',
      row.target || '',
      row.outcome || '',
      row.ip || '',
      row.method || '',
      row.path || '',
      row.host || '',
      row.count || 1,
      detailsText(row.details),
      row.id || '',
    ]));
  }
  return '﻿' + lines.join('\r\n') + '\r\n';
}

// exportFileName is the name of the download: fixed, with the date.
export function exportFileName(now = new Date()) {
  return 'cosmos-audit-' + now.toISOString().slice(0, 10) + '.csv';
}
