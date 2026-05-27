import React from 'react';
import ReactDOM from 'react-dom';
import './index.css';
import flagsmith from '@flagsmith/flagsmith'
import {FlagsmithProvider} from '@flagsmith/flagsmith/react'
import App from './App';
import state from './flagsmith.json'
ReactDOM.render(
  <FlagsmithProvider options={{environmentID: state.environmentID, state}} flagsmith={flagsmith}>
    <App />
  </FlagsmithProvider>,
  document.getElementById('root')
);
