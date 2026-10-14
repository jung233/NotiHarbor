export const reducer = (state, action) => {
  switch (action.type) {
    case 'login':
      return {
        ...state,
        user: action.payload
      };
    case 'logout':
      return {
        ...state,
        user: undefined,
      };
    case 'session-ready':
      return {
        ...state,
        sessionReady: true,
      };

    default:
      return state;
  }
};

export const initialState = {
  user: undefined,
  sessionReady: false,
};
