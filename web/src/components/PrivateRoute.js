import { useContext } from 'react';
import { Navigate, useLocation } from 'react-router-dom';

import { UserContext } from '../context/User';
import Loading from './Loading';


function PrivateRoute({ children }) {
  const [userState] = useContext(UserContext);
  const location = useLocation();
  if (!userState.sessionReady) {
    return <Loading />;
  }
  if (!userState.user) {
    return <Navigate to='/login' replace state={{ from: location }} />;
  }
  return children;
}

export { PrivateRoute };
