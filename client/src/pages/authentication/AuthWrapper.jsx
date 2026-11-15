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
        {/* Fork addition: a flex column (MUI 9's Grid has no column direction: the page scrolled sideways); not a Stack, which would reset the margins */}
        <Box sx={{ display: 'flex', flexDirection: 'column', justifyContent: 'flex-end', minHeight: '100vh' }}>
            <Box sx={{ ml: 3, mt: 3 }}>
                <Logo />
            </Box>
            <Box>
                <Grid
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
            </Box>
            <Box sx={{ m: 3, mt: 1 }}>
                <AuthFooter />
            </Box>
        </Box>
    </Box>
};

AuthWrapper.propTypes = {
    children: PropTypes.node
};

export default AuthWrapper;
