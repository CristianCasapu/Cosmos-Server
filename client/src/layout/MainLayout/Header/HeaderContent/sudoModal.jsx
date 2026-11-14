import React, { useState } from 'react';
import { useFormik } from 'formik';
import { 
  Chip,
  Modal,
  Box,
  Typography,
  TextField,
  Button,
  CircularProgress,
  Alert
} from '@mui/material';
import { useClientInfos } from '../../../../utils/hooks';
import * as API from '../../../../api';
import { CosmosInputPassword } from '../../../../pages/config/users/formShortcuts';
import { useTranslation } from 'react-i18next';
// Fork addition: the prompt also opens by itself when the confirmation is needed in the middle of work, and then keeps the page, see utils/sudoPrompt.js
import { useSudoRequest } from '../../../../utils/sudoPromptHooks';
import { closeSudoRequest, confirmationDone, reloadAfterConfirmation } from '../../../../utils/sudoPrompt';

const SudoModal = () => {
  const [open, setOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const { needsSudo } = useClientInfos();
  const {t} = useTranslation();
  const sudoRequest = useSudoRequest(); // Fork addition
  React.useEffect(() => { if (sudoRequest) setOpen(true); }, [sudoRequest]); // Fork addition
  
  const formik = useFormik({
    initialValues: {
      password: ''
    },
    onSubmit: async (values, { resetForm }) => {
      try {
        setLoading(true);
        await API.auth.sudo(values);
        
        // Fork addition: loading the page again would lose what was entered on it (upstream: always window.location.reload())
        if (reloadAfterConfirmation(sudoRequest)) {
          window.location.reload();
        } else {
          confirmationDone();
        }
        
        setOpen(false);
        resetForm();
      } catch (error) {
        formik.setErrors({ password: 'Invalid password' });
      } finally {
        setLoading(false);
      }
    }
  });

  const handleOpen = () => setOpen(true);
  const handleClose = () => {
    setOpen(false);
    closeSudoRequest(); // Fork addition
    formik.resetForm();
  };

  return (
    (needsSudo || sudoRequest) ? // Fork addition: also when the server said so although this browser's clock does not yet
    <>
      <Chip 
        label="Admin" 
        onClick={handleOpen}
        color="primary"
        style={{height: '36px'}} 
      />
      <Modal
        open={open}
        onClose={handleClose}
      >
        <Box data-sudo-prompt="" sx={{
          position: 'absolute',
          top: '50%',
          left: '50%',
          transform: 'translate(-50%, -50%)',
          width: 400,
          maxWidth: 'calc(100vw - 32px)', // Fork addition: fits a phone
          bgcolor: 'background.paper',
          borderRadius: 2,
          boxShadow: 24,
          p: 4
        }}>
          <Typography variant="h6" component="h2" gutterBottom>
            {t("sudo.title")}
          </Typography>
          <Alert severity="info" sx={{ mt: 2 }}>
            {t("sudo.description")}
          </Alert>
          {/* Fork addition: asked in the middle of work */}
          {sudoRequest && <Alert severity="warning" sx={{ mt: 2 }}>
            {sudoRequest.reason === 'refused'
              ? t('sudo.askedAgain.refused', 'The server did not carry out your last action: the password confirmation has ended or was never given. Confirm it, then repeat the action. What you entered on this page stays as it is.')
              : t('sudo.askedAgain.guard', 'This function needs your password confirmed. What you entered on this page stays as it is.')}
          </Alert>}
          <form onSubmit={formik.handleSubmit}>
            <CosmosInputPassword
              name="password"
              label="Password"
              noStrength
              formik={formik}
              autoComplete="current-password"
            />
            <Box sx={{ mt: 2, display: 'flex', justifyContent: 'flex-end', gap: 1 }}>
              <Button onClick={handleClose}>
                {t("global.cancelAction")}
              </Button>
              <Button 
                type="submit" 
                variant="contained" 
                disabled={loading}
              >
                {loading ? <CircularProgress size={24} /> : 'Submit'}
              </Button>
            </Box>
          </form>
        </Box>
      </Modal>
    </> :
    <></>
  );
};

export default SudoModal;