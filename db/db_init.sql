CREATE TYPE command AS ENUM(
  'ADD',
  'QUOTE',
  'BUY',
  'COMMIT_BUY',
  'CANCEL_BUY',
  'SELL',
  'COMMIT_SELL',
  'CANCEL_SELL',
  'SET_BUY_AMOUNT',
  'CANCEL_SET_BUY',
  'SET_BUY_TRIGGER',
  'SET_SELL_AMOUNT',
  'SET_SELL_TRIGGER',
  'CANCEL_SET_SELL',
  'DUMPLOG',
  'DISPLAY_SUMMARY'
);

CREATE TABLE IF NOT EXISTS users (
  u_id          serial PRIMARY KEY,
  user_name     VARCHAR(20) UNIQUE NOT NULL,
  funds         money
);


-- This table might be moved to redis?
CREATE TABLE IF NOT EXISTS stocks (
  u_id          INT REFERENCES users(u_id),
  stock_symbol  VARCHAR(3),
  amount        NUMERIC
);

-- This table will be moved to the audit server
CREATE TABLE IF NOT EXISTS transactions (
  t_id          serial PRIMARY KEY,
  u_id          INT REFERENCES users(u_id),
  command       command,
  crypto_key    VARCHAR(20),
  amount        NUMERIC,
  stock_symbol  VARCHAR(3),
  price         money,
  time          TIMESTAMP
);