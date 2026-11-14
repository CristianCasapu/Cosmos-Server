import * as React from 'react';
import Table from '@mui/material/Table';
import TableBody from '@mui/material/TableBody';
import TableCell from '@mui/material/TableCell';
import TableContainer from '@mui/material/TableContainer';
import TableHead from '@mui/material/TableHead';
import TableRow from '@mui/material/TableRow';
import Paper from '@mui/material/Paper';
import { CircularProgress, Input, InputAdornment, Stack, TextField, useMediaQuery } from '@mui/material';
import { SearchOutlined } from '@ant-design/icons';
import { useTheme } from '@mui/material/styles';
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { TableEmpty, TableNoMatch, cellLabel, useCellNames } from './tableEmpty'; // Fork addition: empty lists, search without a match, rows as cards on a phone

// Fork addition: a cell that shows nothing (an empty description) or only a picture (a column marked
// `decorative`, e.g. a small chart) is not a link of its own
const emptyCell = (content) => content === null || content === undefined || content === false || content === '';

const PrettyTableView = ({ isLoading, getKey, data, columns, sort, onRowClick, linkTo, buttons, fullWidth, empty, phoneCards = true }) => {
  const { t } = useTranslation();
  const [search, setSearch] = React.useState('');
  const tableRef = React.useRef(null); // Fork addition: names for the controls in cells
  useCellNames(tableRef);
  const theme = useTheme();
  const isDark = theme.palette.mode === 'dark';
  const screenMin = {
    xs: useMediaQuery((theme) => theme.breakpoints.up('xs')),
    sm: useMediaQuery((theme) => theme.breakpoints.up('sm')),
    md: useMediaQuery((theme) => theme.breakpoints.up('md')),
    lg: useMediaQuery((theme) => theme.breakpoints.up('lg')),
    xl: useMediaQuery((theme) => theme.breakpoints.up('xl')),
    xxl: useMediaQuery((theme) => theme.breakpoints.up('xxl')),
  }
  const screenMax = {
    xs: useMediaQuery((theme) => theme.breakpoints.down('xs')),
    sm: useMediaQuery((theme) => theme.breakpoints.down('sm')),
    md: useMediaQuery((theme) => theme.breakpoints.down('md')),
    lg: useMediaQuery((theme) => theme.breakpoints.down('lg')),
    xl: useMediaQuery((theme) => theme.breakpoints.down('xl')),
    xxl: useMediaQuery((theme) => theme.breakpoints.down('xxl')),
  }

  return (
    <Stack direction="column" spacing={2} style={{width: fullWidth ? '100%': ''}}>
      <Stack direction="row" spacing={2} className="ct-table-tools">
        <Input placeholder={t('global.searchPlaceholder')}
          value={search}
          inputProps={{ 'aria-label': t('global.searchPlaceholder') }}
          style={{
            width: '300px',
            maxWidth: '100%', // Fork addition
            borderRadius: '8px',
          }}
          startAdornment={
            <InputAdornment position="start">
              <SearchOutlined />
            </InputAdornment>
          }
          onChange={(e) => {
            setSearch(e.target.value);
          }}
        />
        {buttons}
      </Stack>
      
      {isLoading && (<div style={{height: '550px', display: 'flex', alignItems: 'center', justifyContent: 'center'}}>
                    <CircularProgress />
            </div>
            )}


      {/* Fork addition: the card and its header take the skin's surfaces (were a neutral grey on the navy page);
          on a phone each row is a card of its own (ct-table-cards, skin), with the roles kept for screen readers */}
      {!isLoading && <TableContainer className="ct-table-card" style={{
        width: fullWidth ? '100%': '',
      }} component={Paper} elevation={0}>
        <Table ref={tableRef} className={phoneCards && columns.filter((c) => c && (!c.screenMin || screenMin[c.screenMin]) && (!c.screenMax || screenMax[c.screenMax])).length > 2 ? 'ct-table-cards' : undefined} role="table">{/* (a table of one or two columns fits a phone as it is) */}
          <TableHead>
            <TableRow role="row" sx={{
              '& .MuiTableCell-head': { fontWeight: 600 },
            }}>
              {columns.filter(c=>c).map((column) => (
                ((!column.screenMin || screenMin[column.screenMin]) &&
                (!column.screenMax || screenMax[column.screenMax]) &&
                <TableCell role="columnheader">{column.title}</TableCell>)
              ))}
            </TableRow>
          </TableHead>
          <TableBody>
            {(!data || data.length === 0) && <TableEmpty colSpan={columns.length} empty={empty} />}
            {/* Fork addition: a search that leaves nothing says so */}
            {data && data.length > 0 && search && search.length > 2 && !data.some((row) => columns.filter(c=>c).some((column) => column.search && column.search(row).toLowerCase().includes(search.toLowerCase()))) &&
              <TableNoMatch colSpan={columns.length} search={search} onClear={() => setSearch('')} />}
            {(data || [])
              .filter((row) => {
                if (!search || search.length <= 2) return true;
                let found = false;
                columns.filter(c=>c).forEach((column) => {
                  if (column.search && column.search(row).toLowerCase().includes(search.toLowerCase())) {
                    found = true;
                  }
                })
                return found;
              })
              .sort((a, b) => {
                if (!sort) return 0;
                return sort(a, b);
              })
              .map((row, key) => (
                <TableRow
                  key={getKey(row, key)}
                  role="row"
                  sx={{
                    cursor: 'pointer',
                    borderLeft: '2px solid transparent',
                    transition: 'background 0.15s ease',
                    '&:last-child td, &:last-child th, &:last-child .MuiTableCell-root': { border: 0 },
                    '&:hover': {
                      backgroundColor: 'var(--ct-hover)', // Fork addition: the skin's hover and accent
                      borderColor: 'var(--ct-accent)',
                      '&:hover .emphasis': {
                        textDecoration: 'underline'
                      }
                    },
                  }}
                >
                  {columns.filter(c=>c).map((column) => (
                
                    ((!column.screenMin || screenMin[column.screenMin]) && 
                    (!column.screenMax || screenMax[column.screenMax]) &&
                    <TableCell 
                      role={(linkTo && !column.clickable) ? undefined : 'cell'}
                      data-label={cellLabel(column)}
                      {...((linkTo && !column.clickable && (!cellLabel(column) || column.decorative || emptyCell(column.field(row, key)))) ? { tabIndex: -1, 'aria-hidden': true } : {})/* Fork addition: a cell without a column name (the icon) or without content repeats the row's link: out of the Tab order */}
                      component={(linkTo && !column.clickable) ? Link : 'td'}
                      onClick={() => !column.clickable && onRowClick && onRowClick(row, key)}
                      to={linkTo && linkTo(row, key)}
                      className={column.underline ? 'emphasis' : ''}
                      sx={{
                        textDecoration: 'none',

                        
                        ...column.style,
                      }}>
                        {column.field(row, key)}
                    </TableCell>)
                  ))}
                </TableRow>
              ))}
          </TableBody>
        </Table>
      </TableContainer>}
    </Stack>
  )
}

export default PrettyTableView;