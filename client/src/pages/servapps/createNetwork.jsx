// material-ui
import * as React from 'react';
import { Alert, Button, Checkbox, FormControl, FormHelperText, Grid, InputLabel, MenuItem, Select, Stack, TextField } from '@mui/material';
import { PlusCircleOutlined } from '@ant-design/icons';
import Dialog from '@mui/material/Dialog';
import DialogActions from '@mui/material/DialogActions';
import DialogContent from '@mui/material/DialogContent';
import DialogContentText from '@mui/material/DialogContentText';
import DialogTitle from '@mui/material/DialogTitle';
import { Button as LoadingButton } from '@mui/material'; // Fork addition: MUI 9 has no LoadingButton (Button has the loading state since 6.4)
import { useFormik, FormikProvider } from 'formik';
import * as Yup from 'yup';

import * as API from '../../api';
import { CosmosCheckbox } from '../config/users/formShortcuts';
import ResponsiveButton from '../../components/responseiveButton';
import { useTranslation } from 'react-i18next';
import PermissionGuard from '../../components/permissionGuard';
import { PERM_RESOURCES } from '../../utils/permissions';
import { ForkNetworkFields, networkErrors, useTakenNames } from './forkSmallForms'; // Fork addition
import { keepOnBackdrop } from '../../components/formHelpers'; // Fork addition


const NewNetworkButton = ({ fullWidth, refresh }) => {
  const { t } = useTranslation();
  const [isOpened, setIsOpened] = React.useState(false);
  const taken = useTakenNames('network', isOpened); // Fork addition

  const formik = useFormik({
    initialValues: {
      name: '',
      driver: 'bridge',
      attachCosmos: false,
      parentInterface: '',
      subnet: '',
    },
    // Fork addition: Docker's name rule, the names that exist, the subnet and the parent card, see forkSmallForms.jsx
    validate: (values) => {
      const errors = networkErrors(values, taken);
      return Object.fromEntries(Object.entries(errors).map(([k, v]) => [k, t(v[0], v[1], v[2] || {})]));
    },
    onSubmit: (values, { setErrors, setStatus, setSubmitting }) => {
      setSubmitting(true);
      return API.docker.createNetwork(values)
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
      <Dialog open={isOpened} onClose={keepOnBackdrop(() => setIsOpened(false))} maxWidth="sm" fullWidth>{/* Fork addition: full width on a phone, kept on a click beside it */}
        <FormikProvider value={formik}>
          <DialogTitle>{t('mgmt.servapps.networks.list.newNetwork')}</DialogTitle>
          <DialogContent>
            <DialogContentText>
              <form onSubmit={formik.handleSubmit}>
                <Stack spacing={2} style={{ marginTop: '10px' }}>
                  {/* Fork addition: the fields guided, see forkSmallForms.jsx (upstream: free text, five drivers of which three cannot be created) */}
                  <ForkNetworkFields formik={formik} taken={taken} />
                  <CosmosCheckbox
                    name="attachCosmos"
                    label={t('mgmt.servapps.networks.attackNetwork')}
                    formik={formik}
                  />
                </Stack>
              </form>
              {formik.errors.submit && (
                <Grid size={{ xs: 12 }}>
                  <FormHelperText error>{formik.errors.submit}</FormHelperText>
                </Grid>
              )}
            </DialogContentText>
          </DialogContent>
          <DialogActions>
            <Button onClick={() => setIsOpened(false)}>{t('global.cancelAction')}</Button>
            <LoadingButton
              disabled={formik.errors.submit}
              onClick={formik.handleSubmit}
              loading={formik.isSubmitting}
            >
              {t('global.createAction')}
            </LoadingButton>
          </DialogActions>
        </FormikProvider>
      </Dialog>
      <PermissionGuard permission={PERM_RESOURCES}><ResponsiveButton
        fullWidth={fullWidth}
        onClick={() => setIsOpened(true)}
        startIcon={<PlusCircleOutlined />}
      >
        {t('mgmt.servapps.networks.list.newNetwork')}
      </ResponsiveButton></PermissionGuard>
    </>
  );
};

export default NewNetworkButton;
