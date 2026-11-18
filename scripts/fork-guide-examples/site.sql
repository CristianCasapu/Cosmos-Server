-- Fork addition (plan 50): the site database of the guide's PHP example, with what a plain dump
-- gets wrong: foreign keys with the child table sorted first, a view, a trigger, a function,
-- utf8mb4 text, binary data and a table of 200 MB (MariaDB's sequence engine makes its rows).
CREATE DATABASE IF NOT EXISTS site CHARACTER SET utf8mb4;
USE site;
CREATE TABLE customers (id INT PRIMARY KEY, name VARCHAR(100), note TEXT) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE TABLE orders (id INT PRIMARY KEY, customer_id INT, total DECIMAL(10,2), FOREIGN KEY (customer_id) REFERENCES customers(id)) ENGINE=InnoDB;
CREATE TABLE a_lines (id INT PRIMARY KEY, order_id INT, data BLOB, FOREIGN KEY (order_id) REFERENCES orders(id)) ENGINE=InnoDB;
CREATE TABLE audit (id INT AUTO_INCREMENT PRIMARY KEY, what VARCHAR(50)) ENGINE=InnoDB;
CREATE TRIGGER orders_audit AFTER INSERT ON orders FOR EACH ROW INSERT INTO audit (what) VALUES (CONCAT('order ', NEW.id));
INSERT INTO customers VALUES (1, CONCAT('Ana ', CONVERT(UNHEX('F09F9880') USING utf8mb4)), 'café ''quoted'' \\ back'), (2, 'Bob', NULL);
INSERT INTO orders VALUES (1, 1, 12.50), (2, 1, 3.10), (3, 2, 99.99);
INSERT INTO a_lines VALUES (1, 1, UNHEX('00FF10E2C328')), (2, 3, NULL);
CREATE VIEW big_customers AS SELECT c.name, SUM(o.total) AS t FROM customers c JOIN orders o ON o.customer_id = c.id GROUP BY c.name;
CREATE FUNCTION double_it(x INT) RETURNS INT DETERMINISTIC RETURN x * 2;
CREATE TABLE big (id INT PRIMARY KEY, body VARCHAR(1000)) ENGINE=InnoDB;
INSERT INTO big SELECT seq, REPEAT('x', 1000) FROM seq_1_to_200000;
