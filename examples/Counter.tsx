import React, { useState } from 'react';

interface CounterProps {
  initialValue?: number;
  step?: number;
}

const Counter: React.FC<CounterProps> = ({ 
  initialValue = 0, 
  step = 1 
}) => {
  const [count, setCount] = useState<number>(initialValue);

  const increment = () => {
    setCount(prevCount => prevCount + step);
  };

  const decrement = () => {
    setCount(prevCount => prevCount - step);
  };

  const reset = () => {
    setCount(initialValue);
  };

  return (
    <div style={{
      display: 'flex',
      flexDirection: 'column',
      alignItems: 'center',
      gap: '1rem',
      padding: '2rem',
      border: '1px solid #ccc',
      borderRadius: '8px',
      maxWidth: '300px',
      margin: '2rem auto',
      backgroundColor: '#f9f9f9'
    }}>
      <h2 style={{ margin: 0, color: '#333' }}>Counter</h2>
      
      <div style={{
        fontSize: '3rem',
        fontWeight: 'bold',
        color: count > 0 ? '#2ecc71' : count < 0 ? '#e74c3c' : '#3498db'
      }}>
        {count}
      </div>

      <div style={{
        display: 'flex',
        gap: '0.5rem',
        flexWrap: 'wrap',
        justifyContent: 'center'
      }}>
        <button
          onClick={decrement}
          style={{
            padding: '0.5rem 1rem',
            fontSize: '1rem',
            backgroundColor: '#e74c3c',
            color: 'white',
            border: 'none',
            borderRadius: '4px',
            cursor: 'pointer',
            minWidth: '100px'
          }}
          aria-label="Decrement counter"
        >
          Decrement (-{step})
        </button>
        
        <button
          onClick={increment}
          style={{
            padding: '0.5rem 1rem',
            fontSize: '1rem',
            backgroundColor: '#2ecc71',
            color: 'white',
            border: 'none',
            borderRadius: '4px',
            cursor: 'pointer',
            minWidth: '100px'
          }}
          aria-label="Increment counter"
        >
          Increment (+{step})
        </button>
      </div>

      <button
        onClick={reset}
        style={{
          padding: '0.5rem 1rem',
          fontSize: '1rem',
          backgroundColor: '#3498db',
          color: 'white',
          border: 'none',
          borderRadius: '4px',
          cursor: 'pointer',
          minWidth: '100px'
        }}
        aria-label="Reset counter to initial value"
      >
        Reset
      </button>

      <div style={{
        fontSize: '0.9rem',
        color: '#666',
        textAlign: 'center',
        marginTop: '1rem'
      }}>
        <div>Step size: {step}</div>
        <div>Initial value: {initialValue}</div>
      </div>
    </div>
  );
};

export default Counter;