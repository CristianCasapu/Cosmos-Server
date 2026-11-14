// Fork addition: what a table of PrettyTableView shows when it has no row:
// what the list is for and how it gets filled (the page's own button), or,
// after a search, that nothing matches. `empty` is per page:
//   { title, text, icon, action }   or a React node
// Without it a calm general sentence is shown.
import * as React from 'react';
import { Button, TableCell, TableRow } from '@mui/material';
import { InboxOutlined, SearchOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import EmptyState from '../emptyState';

// the column's name for the card layout on a phone, when it is plain text
export const cellLabel = (column) => (column && typeof column.title === 'string' && column.title.trim() ? column.title : undefined);

export const TableEmpty = ({ colSpan, empty }) => {
  const { t } = useTranslation();
  const custom = empty && React.isValidElement(empty) ? empty : null;
  const spec = !custom && empty && typeof empty === 'object' ? empty : {};
  return <TableRow className="ct-table-empty">
    <TableCell colSpan={colSpan}>
      {custom || <EmptyState compact titleComponent="p" icon={spec.icon || <InboxOutlined />} title={spec.title || t('global.emptyList.title', 'Nothing here yet')} action={spec.action}>
        {spec.text || t('global.emptyList.text', 'This list fills in by itself once there is something to show.')}
      </EmptyState>}
    </TableCell>
  </TableRow>;
};

export const TableNoMatch = ({ colSpan, search, onClear }) => {
  const { t } = useTranslation();
  return <TableRow className="ct-table-empty">
    <TableCell colSpan={colSpan}>
      <EmptyState compact titleComponent="p" icon={<SearchOutlined />} title={t('global.noMatch.title', 'No match')}
        action={<Button size="small" variant="outlined" onClick={onClear}>{t('global.noMatch.clear', 'Clear the search')}</Button>}>
        {t('global.noMatch.text', 'Nothing in this list contains “{{search}}”.', { search: search.slice(0, 80), interpolation: { escapeValue: false } })}
      </EmptyState>
    </TableCell>
  </TableRow>;
};

// A checkbox, switch or field in a table cell usually has no name of its own:
// its column's title is what a sighted user reads. Give it that name, with the
// row's first words ("Enabled: Jellyfin"), when it has none. Run after every
// render of the table (cheap: a query over the table's own cells).
export const useCellNames = (tableRef) => {
  React.useEffect(() => {
    const table = tableRef.current;
    if (!table) return;
    for (const cell of table.querySelectorAll('td[data-label], a[data-label]')) {
      const label = cell.getAttribute('data-label');
      if (!label) continue;
      const row = cell.closest('tr');
      const first = row ? (row.innerText || '').trim().split('\n').map((s) => s.trim()).filter(Boolean)[0] : '';
      for (const control of cell.querySelectorAll('input:not([type=hidden]), button, [role=button], [role=combobox]')) {
        const id = control.id;
        const labelled = control.getAttribute('aria-label') || control.getAttribute('aria-labelledby') || control.getAttribute('title') ||
          (control.tagName === 'BUTTON' && (control.innerText || '').trim()) || (id && table.ownerDocument.querySelector('label[for="' + CSS.escape(id) + '"]'));
        if (labelled && !control.hasAttribute('data-ct-named')) continue;
        control.setAttribute('aria-label', first && first !== label ? label + ': ' + first.slice(0, 60) : label);
        control.setAttribute('data-ct-named', '');
      }
    }
  });
};
