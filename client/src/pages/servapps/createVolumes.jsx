import React, { useState } from 'react';
import { FormikProvider, useFormik } from 'formik';
import * as Yup from 'yup';
import {
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogContentText,
  DialogTitle,
  TextField,
  FormControl,
  InputLabel,
  Select,
  MenuItem,
  Grid,
  FormHelperText,
} from '@mui/material';
import { PlusCircleOutlined } from '@ant-design/icons';
import { Button as LoadingButton } from '@mui/material'; // Fork addition: MUI 9 has no LoadingButton (Button has the loading state since 6.4)
import * as API from '../../api';
import { useTranslation } from 'react-i18next';
import PermissionGuard from '../../components/permissionGuard';
import { PERM_RESOURCES } from '../../utils/permissions';
import { ForkVolumeFields, volumeErrors, useTakenNames } from './forkSmallForms'; // Fork addition
import { keepOnBackdrop } from '../../components/formHelpers'; // Fork addition

const NewVolumeButton = ({ fullWidth, refresh }) => {
  const { t } = useTranslation();
  const [isOpened, setIsOpened] = useState(false);
  const taken = useTakenNames('volume', isOpened); // Fork addition
  const formik = useFormik({
    initialValues: {
      name: '',
      driver: 'local',
    },
    // Fork addition: Docker's name rule and the names that exist, see forkSmallForms.jsx
    validate: (values) => {
      const errors = volumeErrors(values, taken);
      return Object.fromEntries(Object.entries(errors).map(([k, v]) => [k, t(v[0], v[1], v[2] || {})]));
    },
    onSubmit: (values, { setErrors, setStatus, setSubmitting }) => {
      setSubmitting(true);
      return API.docker.createVolume(values)
        .then((res) => {
          setStatus({ success: true });
          setSubmitting(false);
          setIsOpened(false);
          refresh && refresh();
        }).catch((err) => {
          setStatus({ success: false });
          setErrors({ submit: err.message });
          setSubmitting(false);
        });
    },
  });

  return (
    <>
      <Dialog open={isOpened} onClose={keepOnBackdrop(() => setIsOpened(false))} maxWidth="sm" fullWidth>{/* Fork addition */}
        <FormikProvider value={formik}>
          <DialogTitle>{t('mgmt.servApps.volumes.newVolumeTitle')}</DialogTitle>
          <DialogContent>
            <DialogContentText></DialogContentText>
            <form onSubmit={formik.handleSubmit}>
              {/* Fork addition: the name checked and the driver said in words, see forkSmallForms.jsx (upstream: a select with one driver) */}
              <ForkVolumeFields formik={formik} taken={taken} />
            </form>
            {formik.errors.submit && (
              <Grid size={{ xs: 12 }}>
                <FormHelperText error>{formik.errors.submit}</FormHelperText>
              </Grid>
            )}
          </DialogContent>
          <DialogActions>
            <Button onClick={() => setIsOpened(false)}>{t('global.cancelAction')}</Button>
            <LoadingButton
              onClick={formik.handleSubmit}
              loading={formik.isSubmitting}
            >
              {t('global.createAction')}
            </LoadingButton>
          </DialogActions>
        </FormikProvider>
      </Dialog>
      <PermissionGuard permission={PERM_RESOURCES}><Button
        fullWidth={fullWidth}
        onClick={() => setIsOpened(true)}
        startIcon={<PlusCircleOutlined />}
      >
        {t('mgmt.servApps.volumes.newVolumeTitle')}
      </Button></PermissionGuard>
    </>
  );
};

export default NewVolumeButton;