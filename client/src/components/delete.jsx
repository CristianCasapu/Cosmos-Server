import { CheckOutlined, ClockCircleOutlined, DashboardOutlined, DeleteOutlined, DownOutlined, LockOutlined, SafetyOutlined, UpOutlined } from "@ant-design/icons";
import { Card, Chip, IconButton, Stack, Tooltip } from "@mui/material";
import { useState } from "react";
import { useTheme } from '@mui/material/styles';
import { useTranslation } from 'react-i18next';

// Fork addition: both delete buttons carry a tooltip (optional `tooltip` prop, default "Delete")
export const DeleteButton = ({onDelete, disabled, size, tooltip}) => {
  const [confirmDelete, setConfirmDelete] = useState(false);
  const { t } = useTranslation();

  return (<>
    {!confirmDelete && (<Tooltip title={tooltip || t('global.delete', 'Delete')}><Chip label={<DeleteOutlined size={size} color={disabled ? 'silver' : 'error'}/>} 
      onClick={() => !disabled && setConfirmDelete(true)}/></Tooltip>)}
    {confirmDelete && (<Tooltip title={t('tooltip.confirmDelete', 'Click again to confirm')}><Chip label={<CheckOutlined  size={size}/>} color={disabled ? 'silver' : 'error'}
      onClick={(event) => !disabled && onDelete(event)}/></Tooltip>)}
  </>);
}
export const DeleteIconButton = ({onDelete, disabled, size, tooltip}) => {
  const [confirmDelete, setConfirmDelete] = useState(false);
  const { t } = useTranslation();

  return (<>
    {!confirmDelete && (<Tooltip title={tooltip || t('global.delete', 'Delete')}><IconButton color={disabled ? 'silver' : 'error'} onClick={() => !disabled && setConfirmDelete(true)}>
      <DeleteOutlined size={size} />
    </IconButton></Tooltip>)} 
    {confirmDelete && (<Tooltip title={t('tooltip.confirmDelete', 'Click again to confirm')}><IconButton color="error" onClick={(event) => !disabled && onDelete(event)}>
      <CheckOutlined size={size} />
    </IconButton></Tooltip>)}
  </>);
}