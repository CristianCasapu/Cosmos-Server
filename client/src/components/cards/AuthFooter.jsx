// material-ui
import { useMediaQuery, Container, Link, Typography, Stack } from '@mui/material';

// ==============================|| FOOTER - AUTHENTICATION ||============================== //

const AuthFooter = () => {
    const matchDownSM = useMediaQuery((theme) => theme.breakpoints.down('sm'));

    return (
        <Container maxWidth="xl">
            <Stack
                direction={matchDownSM ? 'column' : 'row'}
                sx={{ justifyContent: matchDownSM ? 'center' : 'space-between', textAlign: matchDownSM ? 'center' : 'inherit' }}
                spacing={2}
            >
            </Stack>
        </Container>
    );
};

export default AuthFooter;
