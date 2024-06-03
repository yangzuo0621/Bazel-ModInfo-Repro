from flask import Flask, send_file

app = Flask(__name__)

@app.route('/download/<path:filename>')
def download_file(filename):
    filepath = f"downloads/{filename}"
    return send_file(filepath, as_attachment=True)

if __name__ == '__main__':
    app.run(debug=True)