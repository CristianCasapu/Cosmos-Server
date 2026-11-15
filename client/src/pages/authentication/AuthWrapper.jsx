import PropTypes from 'prop-types';

// material-ui
import { Box, Grid } from '@mui/material';

// project import
import AuthCard from './AuthCard';
import Logo from '../../components/Logo';
import AuthFooter from '../../components/cards/AuthFooter';

// assets
import AuthBackground from '../../assets/images/auth/AuthBackground';
import { useTheme } from '@mui/material/styles';

// ==============================|| AUTHENTICATION - WRAPPER ||============================== //

const AuthWrapper = ({ children }) => {
    const theme = useTheme();
    const darkMode = theme.palette.mode === 'dark';

    return <Box sx={{ minHeight: '100vh', 
        background:  darkMode ? 'none' : '#fafafb' }}>
        <AuthBackground />
        <Grid
            container
            direction="column"
            sx={{ justifyContent: 'flex-end',
                minHeight: '100vh'
            }}
        >
            <Grid size={{ xs: 12 }} sx={{ ml: 3, mt: 3 }}>
                <Logo />
            </Grid>
            <Grid size={{ xs: 12 }}>
                <Grid

 size={{ xs: 12 }}
                    container
                    sx={{ justifyContent: 'center', alignItems: 'center', 
                        minHeight: { 
                            xs: 'calc(100vh - 134px)',
                            md: 'calc(100vh - 112px)'
                        } 
                    }}
                >
                    <Grid>
                        <AuthCard>{children}</AuthCard>
                    </Grid>
                </Grid>
            </Grid>
            <Grid size={{ xs: 12 }} sx={{ m: 3, mt: 1 }}>
                <AuthFooter />
            </Grid>
        </Grid>
    </Box>
};

AuthWrapper.propTypes = {
    children: PropTypes.node
};

export default AuthWrapper;
